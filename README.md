# Ergo Framework #

Implementation of Erlang/OTP node in Go

### Purpose ###

The goal of this project is leverage Erlang/OTP experience by Golang performance. *Ergo Framework* implements OTP design patterns such as `GenServer`/`Supervisor`/`Application` and makes you able to create high performance and reliable application having native integration with Erlang infrastructure

### Features ###

 * Erlang node (run single/[multinode](#multinode))
 * [embedded EPMD](#epmd) (in order to rid erlang' dependencies)
 * Spawn Erlang-like processes
 * Register/unregister processes with simple atom
 * `GenServer` behavior support (with atomic state)
 * `Supervisor` behavior support (with all known restart strategies support)
 * `Application` behavior support
 * Connect to (accept connection from) any Erlang node within a cluster (or clusters, for running as multinode)
 * Making sync/async request in fashion of `gen_server:call` or `gen_server:cast`
 * Monitor processes/nodes
    - local -> local
    - local -> remote
    - remote -> local
 * Link processes
    - local <-> local
    - local <-> remote
    - remote <-> local
 * RPC callbacks support
 * basic [Observer support](#observer)
 * Support Erlang 21.*

### Requirements ###

 * Go 1.10 and above

### EPMD ###

*Ergo Framework* has embedded EPMD implementation in order to run your node without external epmd process needs. But it works as a client with erlang' epmd daemon or others ergo's nodes either.

The one thing makes embedded EPMD different is behavior of handling connection hangs - if ergo' node is running as a epmd client and lost connection it tryes to run its own embedded EPMD service

As an extra option we provide EPMD service as a standalone application. There is simple drop-in replacement of the original Erlang' epmd daemon.

`go get -u github.com/halturin/ergo/cmd/epmd`

### Multinode ###

 This feature allows create two or more nodes within single running instance. The only needs is specify the different set of options for creating nodes (such as: node name, empd port number, secret cookie). You may also want to use this feature to create 'proxy'-node between some clusters. 
 
 Here is a short example how to create two nodes:
 
 ```golang
blablabal
 ```

### Observer ###

 Allows you to see the most of metrics using standard tool of Erlang distribution. Example below shows this feature in action using one of example:

 ... put here gif-ed video demonstrating it us

### Changelog ###

Here is the changes of latest release. For more details see the ChangeLog

#### [1.0.0](https://github.com/halturin/ergo/releases/tag/1.0.0) - 2019-11-30 ####
 There is a bunch of changes we deliver with this release
 * Completelly reworked (almost from scratch) architecture whole project
 * Implemented linking process feature (in order to support Aplication/Supervisor behaviors)
 * Reworked Monitor-feature. Now it has full-featured support with remote process/nodes
 * Added multinode support
 * Added basic observer support
 * Improved code structure and readability
 * Among the new features we have added new bugs that still uncovered :). So, any feedback/bugreport/contribution is highly appreciated

 ### Quick examples ###

  ... put here the set of examples


See examples/ for more details

  * [demoGenServer](examples/genserver)
  * [demoSupervisor](examples/supervisor)
  * [demoApplication](examples/application)

### Elixir Phoenix Users ###

Users of the Elixir Phoenix framework might encounter timeouts when trying to connect a Phoenix node
to an ergonode node. The reason is that, in addition to global_name_server and net_kernel,
Phoenix attemts to broadcast messages to the pg2 PubSub handler:
https://hexdocs.pm/phoenix/1.1.0/Phoenix.PubSub.PG2.html

To work with Phoenix nodes, you must create and register a dedicated pg2 GenServer, and
spawn it inside your node. Take inspiration from the global_name_server.go for the rest of
the GenServer methods, but the Init must specify the "pg2" atom:

```golang
func (pg2 *pg2Server) Init(args ...interface{}) (state interface{}) {
    pg2.Node.Register(etf.Atom("pg2"), pg2.Self)
    return nil
}

