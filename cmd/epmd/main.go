package main

import (
	"context"
	"encoding/binary"
	"flag"
	"fmt"
	"net"
	"strconv"

	"github.com/halturin/ergo/dist"
)

var (
	Names  bool
	Listen int    = 4369
	Host   string = "127.0.0.1"
	Port   int    = 4369
)

func init() {
	flag.IntVar(&Listen, "listen", 4369, "Let epmd listen to another port than default 4369")
	flag.BoolVar(&Names, "names", false, "List names registered with the currently running epmd")
	flag.StringVar(&Host, "epmd", "127.0.0.1", "(for commands) Hostname with running epmd server")
	flag.IntVar(&Port, "port", 4369, "(for commands) Port with running epmd server")

}

func main() {
	flag.Parse()

	if Names {
		getNames()
		return
	}

	if err := dist.Server(context.TODO(), uint16(Listen)); err != nil {
		panic(err)
	}

	// just sleep forever. until somebody kill this process
	select {}
}

func getNames() {
	dsn := net.JoinHostPort(Host, strconv.Itoa(int(Port)))
	conn, err := net.Dial("tcp", dsn)
	if err != nil {
		panic(err)
	}

	defer conn.Close()

	buf := make([]byte, 2048)
	buf[1] = 1
	buf[2] = dist.EPMD_NAMES_REQ
	conn.Write(buf[0:3])
	if n, err := conn.Read(buf); n < 4 || err != nil {
		panic("malformed response from epmd")
	} else {
		fmt.Printf("epmd: up and running on port %d with data:\n", binary.BigEndian.Uint32(buf[0:4]))
		fmt.Printf("%s", string(buf[4:]))
		buf = buf[n:]

		for {
			n, err = conn.Read(buf)
			if err != nil || n == 0 {
				break
			}
			fmt.Printf("%s", string(buf))
			buf = buf[len(buf):]
		}

	}
}
