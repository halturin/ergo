package ergonode

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/halturin/ergonode/etf"
	"github.com/halturin/ergonode/lib"
)

const (
	startPID = 1000
)

type registerProcessRequest struct {
	name    string
	process Process
}

type registerNameRequest struct {
	name string
	pid  etf.Pid
}

type registerPeer struct {
	name string
	p    peer
}

type routeByPidRequest struct {
	from    etf.Pid
	pid     etf.Pid
	message etf.Term
	retries int
}

type routeByNameRequest struct {
	from    etf.Pid
	name    string
	message etf.Term
	retries int
}

type routeByTupleRequest struct {
	from    etf.Pid
	tuple   etf.Tuple
	message etf.Term
	retries int
}

type registrarChannels struct {
	process           chan registerProcessRequest
	unregisterProcess chan etf.Pid
	name              chan registerNameRequest
	unregisterName    chan string
	peer              chan registerPeer
	unregisterPeer    chan string

	routeByPid   chan routeByPidRequest
	routeByName  chan routeByNameRequest
	routeByTuple chan routeByTupleRequest
}

type registrar struct {
	nextPID  uint32
	nodeName string
	creation byte

	node *Node

	channels registrarChannels

	names     map[string]etf.Pid
	processes map[etf.Pid]Process
	peers     map[string]peer
}

func createRegistrar(node *Node) *registrar {
	r := registrar{
		nextPID:  startPID,
		nodeName: node.FullName,
		creation: byte(1),
		node:     node,
		channels: registrarChannels{
			process:           make(chan registerProcessRequest, 10),
			unregisterProcess: make(chan etf.Pid, 10),
			name:              make(chan registerNameRequest, 10),
			unregisterName:    make(chan string, 10),
			peer:              make(chan registerPeer, 10),
			unregisterPeer:    make(chan string, 10),

			routeByPid:   make(chan routeByPidRequest, 100),
			routeByName:  make(chan routeByNameRequest, 100),
			routeByTuple: make(chan routeByTupleRequest, 100),
		},

		names:     make(map[string]etf.Pid),
		processes: make(map[etf.Pid]Process),
		peers:     make(map[string]peer),
	}
	go r.run()
	return &r
}

func (r *registrar) createNewPID(name string) etf.Pid {
	i := atomic.AddUint32(&r.nextPID, 1)
	return etf.Pid{
		Node:     etf.Atom(r.nodeName),
		Id:       i,
		Serial:   1,
		Creation: byte(r.creation),
	}

}

func (r *registrar) run() {
	for {
		select {
		case p := <-r.channels.process:

			r.processes[p.process.self] = p.process
			if p.name != "" {
				r.names[p.name] = p.process.self
			}

		case up := <-r.channels.unregisterProcess:
			if p, ok := r.processes[up]; ok {
				lib.Log("REGISTRAR unregistering process: %v", p.self)
				close(p.mailBox)
				close(p.ready)
				delete(r.processes, up)
				if (p.name) != "" {
					lib.Log("REGISTRAR unregistering name (%v): %s", p.self, p.name)
					delete(r.names, p.name)
				}
			}

		case n := <-r.channels.name:
			lib.Log("registering name %v", n)
			if _, ok := r.names[n.name]; ok {
				// already registered
				continue
			}
			r.names[n.name] = n.pid

		case un := <-r.channels.unregisterName:
			lib.Log("unregistering name %v", un)
			delete(r.names, un)

		case p := <-r.channels.peer:
			lib.Log("registering peer %v", p)
			if _, ok := r.peers[p.name]; ok {
				// already registered
				continue
			}
			r.peers[p.name] = p.p

		case up := <-r.channels.unregisterPeer:
			lib.Log("unregistering name %v", up)
			// TODO: implement it

		case <-r.node.context.Done():
			lib.Log("Finalizing registrar for %s (total number of processes: %d)", r.nodeName, len(r.processes))
			// FIXME: now its just call Stop function for
			// every single process. should we do that for the gen_servers
			// are running under supervisor?
			for _, p := range r.processes {
				lib.Log("FIN: %#v", p.name)
				p.Stop("normal")
			}
			return
		case bp := <-r.channels.routeByPid:
			lib.Log("sending message by pid %v", bp.pid)
			if bp.retries > 2 {
				// drop this message after 3 attempts to deliver this message
				continue
			}
			if string(bp.pid.Node) == r.nodeName {
				// local route
				p := r.processes[bp.pid]
				p.mailBox <- etf.Tuple{bp.from, bp.message}
				continue
			}

			peer, ok := r.peers[string(bp.pid.Node)]
			if !ok {
				// initiate connection and make yet another attempt to deliver this message
				bp.retries++
				r.channels.routeByPid <- bp
				r.node.connect(bp.pid.Node)
				continue
			}
			peer.send <- []etf.Term{etf.Tuple{SEND, etf.Atom(""), bp.pid}, bp.message}

		case bn := <-r.channels.routeByName:
			lib.Log("sending message by name %v", bn.name)
			if pid, ok := r.names[bn.name]; ok {
				r.route(bn.from, pid, bn.message)
			}

		case bt := <-r.channels.routeByTuple:
			lib.Log("sending message by tuple %v", bt.tuple)
			if bt.retries > 2 {
				// drop this message after 3 attempts to deliver this message
				continue
			}
			to_node := bt.tuple.Element(2).(string)
			to_process_name := bt.tuple.Element(1).(string)
			if to_node == r.nodeName {
				r.route(bt.from, to_process_name, bt.message)
				continue
			}

			peer, ok := r.peers[to_node]
			if !ok {
				// initiate connection and make yet another attempt to deliver this message
				bt.retries++
				r.channels.routeByTuple <- bt
				r.node.connect(etf.Atom(to_node))
				continue
			}
			peer.send <- []etf.Term{etf.Tuple{REG_SEND, bt.from, etf.Atom(""), to_process_name}, bt.message}
		}

	}
}

func (r *registrar) RegisterProcess(object interface{}) Process {
	opts := map[string]interface{}{
		"mailbox-size": DefaultProcessMailboxSize, // size of channel for regular messages
	}
	return r.RegisterProcessExt("", object, opts)
}

func (r *registrar) RegisterProcessExt(name string, object interface{}, opts map[string]interface{}) Process {

	mailbox_size := DefaultProcessMailboxSize
	if size, ok := opts["mailbox-size"]; ok {
		mailbox_size = size.(int)
	}
	ctx, stop := context.WithCancel(r.node.context)
	pid := r.createNewPID(r.nodeName)
	wrapped_stop := func(reason string) {
		lib.Log("STOPPING: %#v with reason: %s", pid, reason)
		stop()
		r.UnregisterProcess(pid)
		r.node.monitor.ProcessTerminated(pid, reason)
	}
	process := Process{
		mailBox: make(chan etf.Tuple, mailbox_size),
		ready:   make(chan bool),
		self:    pid,
		context: ctx,
		Stop:    wrapped_stop,
		name:    name,
		Node:    r.node,
	}
	req := registerProcessRequest{
		name:    name,
		process: process,
	}
	r.channels.process <- req

	return process
}

// UnregisterProcess unregister process by Pid
func (r *registrar) UnregisterProcess(pid etf.Pid) {
	r.channels.unregisterProcess <- pid
}

// RegisterName register associates the name with pid
func (r *registrar) RegisterName(name string, pid etf.Pid) {
	req := registerNameRequest{name: name, pid: pid}
	r.channels.name <- req
}

// UnregisterName unregister named process
func (r *registrar) UnregisterName(name string) {
	r.channels.unregisterName <- name
}

func (r *registrar) RegisterPeer(name string, p peer) {
	req := registerPeer{name: name, p: p}
	r.channels.peer <- req
}

func (r *registrar) UnregisterPeer(name string) {
	r.channels.unregisterPeer <- name
}

// Registered returns a list of names which have been registered using Register
func (r *registrar) RegisteredProcesses() []Process {
	p := make([]Process, len(r.processes))
	i := 0
	for _, process := range r.processes {
		p[i] = process
		i++
	}
	return p
}

// WhereIs returns a Pid of regestered process by given name
func (r *registrar) WhereIs(name string) (etf.Pid, error) {
	var p etf.Pid
	// TODO:
	return p, errors.New("not found")
}

// route incomming message to registered process
func (r *registrar) route(from etf.Pid, to etf.Term, message etf.Term) {

	switch tto := to.(type) {
	case etf.Pid:
		req := routeByPidRequest{
			from:    from,
			pid:     tto,
			message: message,
		}
		r.channels.routeByPid <- req

	case etf.Tuple:
		if len(tto) == 2 {
			req := routeByTupleRequest{
				from:    from,
				tuple:   tto,
				message: message,
			}
			r.channels.routeByTuple <- req
		}

	case string:
		req := routeByNameRequest{
			from:    from,
			name:    tto,
			message: message,
		}
		r.channels.routeByName <- req

	case etf.Atom:
		req := routeByNameRequest{
			from:    from,
			name:    string(tto),
			message: message,
		}
		r.channels.routeByName <- req
	default:
		lib.Log("unknow sender type %#v", tto)
	}
}
