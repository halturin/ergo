package ergo

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"sync/atomic"
	"syscall"

	"github.com/halturin/ergo/dist"
	"github.com/halturin/ergo/etf"
	"github.com/halturin/ergo/lib"

	"net"
	"strconv"
	"strings"
	"time"
)

// Node instance of created node using CreateNode
type Node struct {
	dist.EPMD
	listener net.Listener
	Cookie   string

	registrar *registrar
	monitor   *monitor
	context   context.Context
	Stop      context.CancelFunc

	StartedAt time.Time
	uniqID    int64

	opts NodeOptions
}

// NodeOptions struct with bootstrapping options for CreateNode
type NodeOptions struct {
	ListenRangeBegin  uint16
	ListenRangeEnd    uint16
	Hidden            bool
	EPMDPort          uint16
	DisableEPMDServer bool
	SendQueueLength   int
	FragmentationUnit int
}

const (
	defaultListenRangeBegin uint16 = 15000
	defaultListenRangeEnd   uint16 = 65000
	defaultEPMDPort         uint16 = 4369

	defaultSendQueueLength   int = 100
	defaultFragmentationUnit     = 65000

	versionOTP        int = 21
	versionERTSprefix     = "ergo"
	version               = "1.0.0"
)

// CreateNode create new node with name and cookie string
func CreateNode(name string, cookie string, opts NodeOptions) *Node {
	return CreateNodeWithContext(context.Background(), name, cookie, opts)
}

// CreateNodeWithContext create new node with specified context, name and cookie string
func CreateNodeWithContext(ctx context.Context, name string, cookie string, opts NodeOptions) *Node {

	lib.Log("Start with name '%s' and cookie '%s'", name, cookie)
	nodectx, nodestop := context.WithCancel(ctx)

	node := &Node{
		Cookie:    cookie,
		context:   nodectx,
		Stop:      nodestop,
		StartedAt: time.Now(),
		uniqID:    time.Now().UnixNano(), // (*uint64)(unsafe.Pointer(node)) ?

	}

	// start networking if name is defined
	if name != "" {
		// set defaults
		if opts.ListenRangeBegin == 0 {
			opts.ListenRangeBegin = defaultListenRangeBegin
		}
		if opts.ListenRangeEnd == 0 {
			opts.ListenRangeEnd = defaultListenRangeEnd
		}
		lib.Log("Listening range: %d...%d", opts.ListenRangeBegin, opts.ListenRangeEnd)

		if opts.EPMDPort == 0 {
			opts.EPMDPort = defaultEPMDPort
		}
		if opts.EPMDPort != 4369 {
			lib.Log("Using custom EPMD port: %d", opts.EPMDPort)
		}

		if opts.SendQueueLength == 0 {
			opts.SendQueueLength = defaultSendQueueLength
		}

		if opts.FragmentationUnit < 1500 {
			opts.FragmentationUnit = defaultFragmentationUnit
		}

		if opts.Hidden {
			lib.Log("Running as hidden node")
		}
		ns := strings.Split(name, "@")
		if len(ns) != 2 {
			panic("FQDN for node name is required (example: node@hostname)")
		}

		if listenPort := node.listen(ns[1], opts); listenPort == 0 {
			panic("Can't listen port")
		} else {
			// start EPMD
			node.EPMD.Init(nodectx, name, listenPort, opts.EPMDPort, opts.Hidden, opts.DisableEPMDServer)
		}

	}

	node.opts = opts

	node.registrar = createRegistrar(node)
	node.monitor = createMonitor(node)

	netKernelSup := &netKernelSup{}
	node.Spawn("net_kernel_sup", ProcessOptions{}, netKernelSup)

	return node
}

// Spawn create new process
func (n *Node) Spawn(name string, opts ProcessOptions, object interface{}, args ...interface{}) (*Process, error) {

	process, err := n.registrar.RegisterProcessExt(name, object, opts)
	if err != nil {
		return nil, err
	}

	go func() {
		pid := process.Self()

		defer func() {
			if r := recover(); r != nil {
				fmt.Printf("Warning: recovered process: %v %#v\n", process.self, r)
				n.registrar.UnregisterProcess(pid)
				n.monitor.ProcessTerminated(pid, etf.Atom(name), "panic")
				process.Kill()
			}
			close(process.ready)
		}()

		reason := object.(ProcessBehaviour).loop(process, object, args...)
		n.registrar.UnregisterProcess(pid)
		n.monitor.ProcessTerminated(pid, etf.Atom(name), reason)
		if reason != "kill" {
			process.Kill()
		}

	}()

	<-process.ready

	return process, nil
}

// Register register associates the name with pid
func (n *Node) Register(name string, pid etf.Pid) error {
	return n.registrar.RegisterName(name, pid)
}

func (n *Node) Unregister(name string) {
	n.registrar.UnregisterName(name)
}

// IsProcessAlive returns true if the process with given pid is alive
func (n *Node) IsProcessAlive(pid etf.Pid) bool {
	if pid.Node != etf.Atom(n.FullName) {
		return false
	}

	p := n.registrar.GetProcessByPid(pid)
	if p == nil {
		return false
	}

	return p.IsAlive()
}

// IsAlive returns true if node is running
func (n *Node) IsAlive() bool {
	return n.context.Err() == nil
}

// Wait waits until node stopped
func (n *Node) Wait() {
	<-n.context.Done()
}

// WaitWithTimeout waits until node stopped. Return ErrTimeout
// if given timeout is exceeded
func (n *Node) WaitWithTimeout(d time.Duration) error {

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return ErrTimeout
	case <-n.context.Done():
		return nil
	}
}

// ProcessInfo returns the details about given Pid
func (n *Node) ProcessInfo(pid etf.Pid) (ProcessInfo, error) {
	p := n.registrar.GetProcessByPid(pid)
	if p == nil {
		return ProcessInfo{}, fmt.Errorf("undefined")
	}

	return p.Info(), nil
}

func (n *Node) serve(link *dist.Link, opts NodeOptions) error {

	send := make(chan []etf.Term, opts.SendQueueLength)
	p := peer{
		name: link.GetRemoteName(),
		send: send,
	}

	if err := n.registrar.RegisterPeer(p); err != nil {
		// duplicate link?
		return err
	}

	// run writer routine
	go func() {
		defer link.Close()
		defer func() { n.registrar.UnregisterPeer(link.GetRemoteName()) }()

		for {
			select {
			case terms := <-send:
				err := link.WriteMessage(terms)
				if err != nil {
					lib.Log("node error (writing): %s", err.Error())
					return
				}
			case <-n.context.Done():
				return
			}

		}
	}()

	// run reader routine
	go func() {
		var b *lib.Buffer
		var err error
		var packetLength int

		defer link.Close()
		defer func() { n.registrar.UnregisterPeer(link.GetRemoteName()) }()

		// do not run more than the total number of cores
		parallelHandlers := make(chan error, runtime.NumCPU())
		b = lib.TakeBuffer()

		for {
			packetLength, err = link.Read(b)
			if err != nil || packetLength == 0 {
				lib.ReleaseBuffer(b)
				return
			}

			// block if exceed the limits (number of HandlePacket goroutines)
			parallelHandlers <- nil

			// append the tail (part of the next packet)
			b1 := lib.TakeBuffer()
			b1.Set(b.B[packetLength:])

			go func(buf *lib.Buffer) {
				defer func() {
					lib.ReleaseBuffer(buf)
					<-parallelHandlers
				}()
				link.HandlePacket(buf.B[:packetLength])
			}(b)

			// set new buffer as a current for the next reading
			b = b1
		}
	}()

	return nil
}

// LoadedApplications returns a list with information about the
// applications, which are loaded using ApplicatoinLoad
func (n *Node) LoadedApplications() []ApplicationInfo {
	info := []ApplicationInfo{}
	for _, a := range n.registrar.ApplicationList() {
		appInfo := ApplicationInfo{
			Name:        a.Name,
			Description: a.Description,
			Version:     a.Version,
		}
		info = append(info, appInfo)
	}
	return info
}

// WhichApplications returns a list with information about the applications that are currently running.
func (n *Node) WhichApplications() []ApplicationInfo {
	info := []ApplicationInfo{}
	for _, a := range n.registrar.ApplicationList() {
		if a.process == nil {
			// list only started apps
			continue
		}
		appInfo := ApplicationInfo{
			Name:        a.Name,
			Description: a.Description,
			Version:     a.Version,
			PID:         a.process.self,
		}
		info = append(info, appInfo)
	}
	return info
}

// GetApplicationInfo returns information about application
func (n *Node) GetApplicationInfo(name string) (ApplicationInfo, error) {
	spec := n.registrar.GetApplicationSpecByName(name)
	if spec == nil {
		return ApplicationInfo{}, ErrAppUnknown
	}

	pid := etf.Pid{}
	if spec.process != nil {
		pid = spec.process.self
	}

	return ApplicationInfo{
		Name:        name,
		Description: spec.Description,
		Version:     spec.Version,
		PID:         pid,
	}, nil
}

// ApplicationLoad loads the application specification for an application
// into the node. It also loads the application specifications for any included applications
func (n *Node) ApplicationLoad(app interface{}, args ...interface{}) error {

	spec, err := app.(ApplicationBehavior).Load(args...)
	if err != nil {
		return err
	}
	spec.app = app.(ApplicationBehavior)
	for i := range spec.Applications {
		if e := n.ApplicationLoad(spec.Applications[i], args...); e != nil && e != ErrAppAlreadyLoaded {
			return e
		}
	}

	return n.registrar.RegisterApp(spec.Name, &spec)
}

// ApplicationUnload unloads the application specification for Application from the
// node. It also unloads the application specifications for any included applications.
func (n *Node) ApplicationUnload(appName string) error {
	spec := n.registrar.GetApplicationSpecByName(appName)
	if spec == nil {
		return ErrAppUnknown
	}
	if spec.process != nil {
		return ErrAppAlreadyStarted
	}

	n.registrar.UnregisterApp(appName)
	return nil
}

// ApplicationStartPermanent start Application with start type ApplicationStartPermanent
// If this application terminates, all other applications and the entire node are also
// terminated
func (n *Node) ApplicationStartPermanent(appName string, args ...interface{}) (*Process, error) {
	return n.applicationStart(ApplicationStartPermanent, appName, args...)
}

// ApplicationStartTransient start Application with start type ApplicationStartTransient
// If transient application terminates with reason 'normal', this is reported and no
// other applications are terminated. Otherwise, all other applications and node
// are terminated
func (n *Node) ApplicationStartTransient(appName string, args ...interface{}) (*Process, error) {
	return n.applicationStart(ApplicationStartTransient, appName, args...)
}

// ApplicationStart start Application with start type ApplicationStartTemporary
// If an application terminates, this is reported but no other applications
// are terminated
func (n *Node) ApplicationStart(appName string, args ...interface{}) (*Process, error) {
	return n.applicationStart(ApplicationStartTemporary, appName, args...)
}

func (n *Node) applicationStart(startType, appName string, args ...interface{}) (*Process, error) {

	spec := n.registrar.GetApplicationSpecByName(appName)
	if spec == nil {
		return nil, ErrAppUnknown
	}

	spec.startType = startType

	// to prevent race condition on starting application we should
	// make sure that nobodyelse starting it
	spec.mutex.Lock()
	defer spec.mutex.Unlock()

	if spec.process != nil {
		return nil, ErrAppAlreadyStarted
	}

	for _, depAppName := range spec.Applications {
		if _, e := n.ApplicationStart(depAppName); e != nil && e != ErrAppAlreadyStarted {
			return nil, e
		}
	}

	// passing 'spec' to the process loop in order to handle children's startup.
	args = append([]interface{}{spec}, args)
	appProcess, e := n.Spawn("", ProcessOptions{}, spec.app, args...)
	if e != nil {
		return nil, e
	}

	spec.process = appProcess
	return appProcess, nil
}

// ApplicationStop stop running application
func (n *Node) ApplicationStop(name string) error {
	spec := n.registrar.GetApplicationSpecByName(name)
	if spec == nil {
		return ErrAppUnknown
	}

	if spec.process == nil {
		return ErrAppIsNotRunning
	}

	spec.process.Exit(spec.process.Self(), "normal")
	return nil
}

func (n *Node) handleTerms(terms []etf.Term) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("Warning: recovered node.handleTerms: %s\n", r)
		}
	}()

	if len(terms) == 0 {
		// keep alive
		return
	}

	lib.Log("Node terms: %#v", terms)

	switch t := terms[0].(type) {
	case etf.Tuple:
		switch act := t.Element(1).(type) {
		case int:
			switch act {
			case distProtoREG_SEND:
				// {6, FromPid, Unused, ToName}
				if len(terms) == 2 {
					n.registrar.route(t.Element(2).(etf.Pid), t.Element(4), terms[1])
				} else {
					lib.Log("*** ERROR: bad REG_SEND: %#v", terms)
				}

			case distProtoSEND:
				// {2, Unused, ToPid}
				// SEND has no sender pid
				n.registrar.route(etf.Pid{}, t.Element(3), terms[1])

			case distProtoLINK:
				// {1, FromPid, ToPid}
				lib.Log("LINK message (act %d): %#v", act, t)
				n.monitor.Link(t.Element(2).(etf.Pid), t.Element(3).(etf.Pid))

			case distProtoUNLINK:
				// {4, FromPid, ToPid}
				lib.Log("UNLINK message (act %d): %#v", act, t)
				n.monitor.Unink(t.Element(2).(etf.Pid), t.Element(3).(etf.Pid))

			case distProtoNODE_LINK:
				lib.Log("NODE_LINK message (act %d): %#v", act, t)

			case distProtoEXIT:
				// {3, FromPid, ToPid, Reason}
				lib.Log("EXIT message (act %d): %#v", act, t)
				terminated := t.Element(2).(etf.Pid)
				reason := fmt.Sprint(t.Element(4))
				n.monitor.ProcessTerminated(terminated, etf.Atom(""), string(reason))

			case distProtoEXIT2:
				lib.Log("EXIT2 message (act %d): %#v", act, t)

			case distProtoMONITOR:
				// {19, FromPid, ToProc, Ref}, where FromPid = monitoring process
				// and ToProc = monitored process pid or name (atom)
				lib.Log("MONITOR message (act %d): %#v", act, t)
				n.monitor.MonitorProcessWithRef(t.Element(2).(etf.Pid), t.Element(3), t.Element(4).(etf.Ref))

			case distProtoDEMONITOR:
				// {20, FromPid, ToProc, Ref}, where FromPid = monitoring process
				// and ToProc = monitored process pid or name (atom)
				lib.Log("DEMONITOR message (act %d): %#v", act, t)
				n.monitor.DemonitorProcess(t.Element(4).(etf.Ref))

			case distProtoMONITOR_EXIT:
				// {21, FromProc, ToPid, Ref, Reason}, where FromProc = monitored process
				// pid or name (atom), ToPid = monitoring process, and Reason = exit reason for the monitored process
				lib.Log("MONITOR_EXIT message (act %d): %#v", act, t)
				terminated := t.Element(2).(etf.Pid)
				reason := fmt.Sprint(t.Element(5))
				// FIXME: we must handle case when 'terminated' is atom
				n.monitor.ProcessTerminated(terminated, etf.Atom(""), string(reason))

			// Not implemented yet, just stubs. TODO.
			case distProtoSEND_SENDER:
				lib.Log("SEND_SENDER message (act %d): %#v", act, t)
			case distProtoSEND_SENDER_TT:
				lib.Log("SEND_SENDER_TT message (act %d): %#v", act, t)
			case distProtoPAYLOAD_EXIT:
				lib.Log("PAYLOAD_EXIT message (act %d): %#v", act, t)
			case distProtoPAYLOAD_EXIT_TT:
				lib.Log("PAYLOAD_EXIT_TT message (act %d): %#v", act, t)
			case distProtoPAYLOAD_EXIT2:
				lib.Log("PAYLOAD_EXIT2 message (act %d): %#v", act, t)
			case distProtoPAYLOAD_EXIT2_TT:
				lib.Log("PAYLOAD_EXIT2_TT message (act %d): %#v", act, t)
			case distProtoPAYLOAD_MONITOR_P_EXIT:
				lib.Log("PAYLOAD_MONITOR_P_EXIT message (act %d): %#v", act, t)

			default:
				lib.Log("Unhandled node message (act %d): %#v", act, t)
			}
		default:
			lib.Log("UNHANDLED ACT: %#v", t.Element(1))
		}
	}
}

// ProvideRPC register given module/function as RPC method
func (n *Node) ProvideRPC(module string, function string, fun rpcFunction) error {
	lib.Log("RPC provide: %s:%s %#v", module, function, fun)
	message := etf.Tuple{
		etf.Atom("$provide"),
		etf.Atom(module),
		etf.Atom(function),
		fun,
	}
	rex := n.registrar.GetProcessByName("rex")
	if rex == nil {
		return fmt.Errorf("RPC module is disabled")
	}

	if v, err := rex.Call(rex.Self(), message); v != etf.Atom("ok") || err != nil {
		return fmt.Errorf("value: %s err: %s", v, err)
	}

	return nil
}

// RevokeRPC unregister given module/function
func (n *Node) RevokeRPC(module, function string) error {
	lib.Log("RPC revoke: %s:%s", module, function)

	rex := n.registrar.GetProcessByName("rex")
	if rex == nil {
		return fmt.Errorf("RPC module is disabled")
	}

	message := etf.Tuple{
		etf.Atom("$revoke"),
		etf.Atom(module),
		etf.Atom(function),
	}

	if v, err := rex.Call(rex.Self(), message); v != etf.Atom("ok") || err != nil {
		return fmt.Errorf("value: %s err: %s", v, err)
	}

	return nil
}

// GetProcessByName returns Process associated with given name
func (n *Node) GetProcessByName(name string) *Process {
	return n.registrar.GetProcessByName(name)
}

// GetProcessByPid returns Process by given pid
func (n *Node) GetProcessByPid(pid etf.Pid) *Process {
	return n.registrar.GetProcessByPid(pid)
}

// GetProcessList returns array of running process
func (n *Node) GetProcessList() []*Process {
	return n.registrar.ProcessList()
}

// MakeRef returns atomic reference etf.Ref within this node
func (n *Node) MakeRef() (ref etf.Ref) {
	ref.Node = etf.Atom(n.FullName)
	ref.Creation = 1
	nt := atomic.AddInt64(&n.uniqID, 1)
	id1 := uint32(uint64(nt) & ((2 << 17) - 1))
	id2 := uint32(uint64(nt) >> 46)
	ref.Id = []uint32{id1, id2, 0}

	return
}

func (n *Node) VersionERTS() string {
	return fmt.Sprintf("%s-%s-%s", versionERTSprefix, version, runtime.Version())
}

func (n *Node) VersionOTP() int {
	return versionOTP
}

func (n *Node) connect(to etf.Atom) error {
	var port int
	var err error
	var dialer = net.Dialer{
		Control: setSocketOptions,
	}
	if port, err = n.ResolvePort(string(to)); port < 0 {
		return fmt.Errorf("Can't resolve port: %s", err)
	}
	ns := strings.Split(string(to), "@")

	c, err := dialer.DialContext(n.context, "tcp", net.JoinHostPort(ns[1], strconv.Itoa(int(port))))
	if err != nil {
		lib.Log("Error calling net.Dialer.DialerContext : %s", err.Error())
		return err
	}

	link, e := dist.Handshake(c, n.FullName, n.Cookie, false)
	if e != nil {
		return e
	}

	if err := n.serve(link, n.opts); err != nil {
		c.Close()
		return err
	}
	return nil
}

func (n *Node) listen(name string, opts NodeOptions) uint16 {

	lc := net.ListenConfig{Control: setSocketOptions}
	for p := opts.ListenRangeBegin; p <= opts.ListenRangeEnd; p++ {
		l, err := lc.Listen(n.context, "tcp", net.JoinHostPort(name, strconv.Itoa(int(p))))
		if err != nil {
			continue
		}
		go func() {
			for {
				c, err := l.Accept()

				lib.Log("Accepted new connection from %s", c.RemoteAddr().String())
				if err != nil {
					if err == context.Canceled {
						return
					}
					lib.Log(err.Error())
					continue
				}

				link, e := dist.HandshakeAccept(c, n.FullName, n.Cookie, opts.Hidden)
				if e != nil {
					lib.Log("Can't handshake with %s: %s", c.RemoteAddr().String(), e)
					c.Close()
					continue
				}

				// start serving this link
				if err := n.serve(link, opts); err != nil {
					lib.Log("Can't serve connection link due to: %s", err)
					c.Close()
				}

			}
		}()

		// return port number this node listenig on for the incoming connections
		return p
	}

	// all the ports within a given range are taken
	return 0
}

func setSocketOptions(network string, address string, c syscall.RawConn) error {
	var fn = func(s uintptr) {
		var setErr error
		setErr = syscall.SetsockoptInt(int(s), syscall.IPPROTO_TCP, syscall.TCP_KEEPINTVL, 5)
		if setErr != nil {
			log.Fatal(setErr)
		}
	}
	if err := c.Control(fn); err != nil {
		return err
	}

	return nil

}
