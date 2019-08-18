package ergonode

import (
	"context"
	"sync"
)

type SupervisorStrategy = string
type SupervisorRestart = string
type SupervisorChild = string

const (
	// Restart strategies:

	// SupervisorStrategyOneForOne If one child process terminates and is to be restarted, only
	// that child process is affected. This is the default restart strategy.
	SupervisorStrategyOneForOne = "one_for_one"

	// SupervisorStrategyOneForAll If one child process terminates and is to be restarted, all other
	// child processes are terminated and then all child processes are restarted.
	SupervisorStrategyOneForAll = "one_for_all"

	// SupervisorStrategyRestForOne If one child process terminates and is to be restarted,
	// the 'rest' of the child processes (that is, the child
	// processes after the terminated child process in the start order)
	// are terminated. Then the terminated child process and all
	// child processes after it are restarted
	SupervisorStrategyRestForOne = "rest_for_one"

	// SupervisorStrategySimpleOneForOne A simplified one_for_one supervisor, where all
	// child processes are dynamically added instances
	// of the same process type, that is, running the same code.
	SupervisorStrategySimpleOneForOne = "simple_one_for_one"

	// Restart types:

	// SupervisorRestartPermanent child process is always restarted
	SupervisorRestartPermanent = "permanent"

	// SupervisorRestartTemporary child process is never restarted
	// (not even when the supervisor restart strategy is rest_for_one
	// or one_for_all and a sibling death causes the temporary process
	// to be terminated)
	SupervisorRestartTemporary = "temporary"

	// SupervisorRestartTransient child process is restarted only if
	// it terminates abnormally, that is, with an exit reason other
	// than normal, shutdown, or {shutdown,Term}.
	SupervisorRestartTransient = "transient"

	// SupervisorChild
	SupervisorChildWorker     = "worker"
	SupervisorChildSupervisor = "supervisor"
)

// SupervisorBehavior interface
type SupervisorBehavior interface {
	StartChild()
	StartLink()
}

// Supervisor is implementation of SupervisorBehavior interface
type Supervisor struct {
	strategy SupervisorStrategy
	restart  SupervisorRestart
	children []interface{}
	process  Process

	Node    *Node // current node of process
	context context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// CreateSupervisor
func CreateSupervisor(childlist []*GenServer, strategy SupervisorStrategy,
	intensity, period int) *Supervisor {

	sv := Supervisor{}
	sv.context, sv.cancel = context.WithCancel(context.Background())

	return &sv
}

func (s *Supervisor) Stop() {
	s.cancel()
}
