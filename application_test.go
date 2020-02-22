package ergonode

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/halturin/ergonode/etf"
)

type testApplication struct {
	Application
}

func (a *testApplication) Load(args ...interface{}) (ApplicationSpec, error) {
	fmt.Println("LOADING")
	lifeSpan := args[0].(time.Duration)
	strategy := args[1].(string)
	return ApplicationSpec{
		Name:        "testapp",
		Description: "My Test Applicatoin",
		Version:     "v.0.1",
		Environment: map[string]interface{}{
			"envName1": 123,
			"envName2": "Hello world",
		},
		Children: []ApplicationChildSpec{
			ApplicationChildSpec{
				Child: &testAppGenServer{},
			},
		},
		Lifespan: lifeSpan,
		Strategy: strategy,
	}, nil
}

func (a *testApplication) Start(p *Process, args ...interface{}) {
	p.SetEnv("MMM", 888)
	fmt.Println("STARTED APP")
}

// test GenServer
type testAppGenServer struct {
	GenServer
}

func (gs *testAppGenServer) Init(args ...interface{}) interface{} {
	fmt.Println("STARTING TEST GS IN APP")
	return nil
}

func (gs *testAppGenServer) HandleCast(message etf.Term, state interface{}) (string, interface{}) {
	return "noreply", state
}

func (gs *testAppGenServer) HandleCall(from etf.Tuple, message etf.Term, state interface{}) (string, etf.Term, interface{}) {
	return "reply", etf.Atom("ok"), nil
}

func (gs *testAppGenServer) Terminate(reason string, state interface{}) {
	fmt.Println("TERMINATING TEST GS IN APP with reason:", reason)
}

// testing
func TestApplication(t *testing.T) {

	fmt.Printf("\n=== Test Application load/unload/start/stop\n")
	fmt.Printf("\nStarting node nodeTestAplication@localhost:")
	ctx := context.Background()
	node := CreateNodeWithContext(ctx, "nodeApplication@localhost", "cookies", NodeOptions{})
	if node == nil {
		t.Fatal("can't start node")
	} else {
		fmt.Println("OK")
	}

	app := &testApplication{}
	lifeSpan := 1 * time.Second

	fmt.Printf("Loading application... ")
	err := node.ApplicationLoad(app, lifeSpan, ApplicationStrategyPermanent)
	if err != nil {
		t.Fatal(err)
	}

	la := node.LoadedApplications()
	if len(la) != 1 {
		t.Fatal("total number of loaded application mismatch")
	}
	if la[0].Name != "testapp" {
		t.Fatal("can't load application")
	}

	fmt.Println("OK")

	wa := node.WhichApplications()
	if len(wa) > 0 {
		t.Fatal("total number of running application mismatch")
	}

	fmt.Printf("Unloading application... ")
	if err := node.ApplicationUnload("testapp"); err != nil {
		t.Fatal(err)
	}

	la = node.LoadedApplications()
	if len(la) > 0 {
		t.Fatal("total number of loaded application mismatch")
	}

	fmt.Println("OK")

	fmt.Printf("Starting application... ")
	if err := node.ApplicationLoad(app, lifeSpan, ApplicationStrategyPermanent); err != nil {
		t.Fatal(err)
	}
	fmt.Println("1")

	p, e := node.ApplicationStart("testapp")
	if e != nil {
		t.Fatal(e)
	}

	// we shouldn't be able to unload running app
	if e := node.ApplicationUnload("testapp"); e != ErrAppAlreadyStarted {
		t.Fatal(e)
	}
	fmt.Println("2")
	wa = node.WhichApplications()
	if len(wa) != 1 {
		t.Fatal("total number of running application mismatch")
	}

	fmt.Println("3")
	if wa[0].Name != "testapp" {
		t.Fatal("can't start application")
	}

	fmt.Println("OK")

	fmt.Printf("Stopping application...")
	if e := node.ApplicationStop("testapp"); e != nil {
		t.Fatal(e)
	}
	fmt.Println("OK")

	fmt.Printf("Starting application with lifespan 150ms...")
	node.ApplicationUnload("testapp")
	lifeSpan = 150 * time.Millisecond
	if err := node.ApplicationLoad(app, lifeSpan, ApplicationStrategyPermanent); err != nil {
		t.Fatal(err)
	}
	p, e = node.ApplicationStart("testapp")
	if e != nil {
		t.Fatal(e)
	}

	time.Sleep(1000 * time.Millisecond)

	fmt.Println("LLLL", node.WhichApplications())
	if node.IsProcessAlive(p.Self()) {
		t.Fatal("application still alive")
	}
	fmt.Println("OK")

	//	node.ApplicationLoad(app, lifeSpan, ApplicationStrategyPermanent)
	//
	//	fmt.Println("LOADED APP", node.LoadedApplications())
	//	fmt.Println("RUNNING APP", node.WhichApplications())
	//
	//	p, e := node.ApplicationStart("testapp")
	//	if e != nil {
	//		fmt.Println("ERR", e)
	//	}
	//	fmt.Println("PROC", p.Self())
	//	fmt.Println("XXX", p.ListEnv())
	//
	//	p.SetEnv("ABB", 1.234)
	//	p.SetEnv("CDF", 567)
	//	p.SetEnv("GHJ", "890")
	//
	//	fmt.Println("LOADED APP", node.LoadedApplications())
	//	fmt.Println("RUNNING APP", node.WhichApplications())
	//
	//	// node.ApplicationStart(app, lifeSpan, ApplicationStrategyTemporary)
	//	// node.ApplicationStart(app, lifeSpan, ApplicationStrategyTransient)
	//
	//	node.Stop()
}
