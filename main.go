package main

import (
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jpillora/opts"
)

//compiler-settable version
var VERSION = "0.0.0-src"

//define cli
var config = struct {
	SocketAddr string `help:"path to unix socket file to listen on"`
	TCPAddr    string `help:"remote tcp socket address to forward to"`
	Quiet      bool   `help:"suppress logs"`
}{
	SocketAddr: "/var/run/fwd.sock",
	TCPAddr:    "127.0.0.1:22",
	Quiet:      false,
}

func main() {
	//init and parse cli
	o := opts.New(&config)
	o.LineWidth = 60
	o.Name("sockfwd").Version(VERSION).Repo("github.com/jpillora/sockfwd").Parse()
	//check socket file
	if info, err := os.Stat(config.SocketAddr); err == nil {
		if info.Mode()&os.ModeSocket != os.ModeSocket {
			log.Fatalf("non-socket file already exists at %s", config.SocketAddr)
		}
		//remove existing socket
		if err := os.Remove(config.SocketAddr); err != nil {
			log.Fatal("failed to remove existing socket")
		}
	} else if err != nil && !os.IsNotExist(err) {
		log.Fatal(err)
	}
	//check tcp address
	if _, err := net.ResolveTCPAddr("tcp", config.TCPAddr); err != nil {
		log.Fatalf("failed to resolve tcp address: %s", err)
	}
	//listen
	l, err := net.Listen("unix", config.SocketAddr)
	if err != nil {
		log.Fatal(err)
	}
	//cleanup before shutdown
	go func() {
		c := make(chan os.Signal)
		signal.Notify(c, os.Interrupt)
		<-c
		l.Close()
		os.Remove(config.SocketAddr)
		logf("closed listener and removed socket")
		os.Exit(0)
	}()
	//accept connections
	logf("listening on %s and forwarding to %s", config.SocketAddr, config.TCPAddr)
	for {
		uconn, err := l.Accept()
		if err != nil {
			logf("accept failed: %s", err)
			continue
		}
		go fwd(uconn)
	}
}

//detailed statistics
var count uint64

func fwd(uconn net.Conn) {
	defer uconn.Close()
	tconn, err := net.Dial("tcp", config.TCPAddr)
	if err != nil {
		log.Printf("local dial failed: %s", err)
		return
	}
	id := atomic.AddUint64(&count, 1)
	logf("[%d] connected", id)
	t0 := time.Now()
	wg := sync.WaitGroup{}
	wg.Add(2)
	go func() {
		io.Copy(uconn, tconn)
		uconn.Close()
		wg.Done()
	}()
	go func() {
		io.Copy(tconn, uconn)
		tconn.Close()
		wg.Done()
	}()
	wg.Wait()
	logf("[%d] disconnected (%s)", id, time.Now().Sub(t0))
}

//silenceable log.Printf
func logf(format string, args ...interface{}) {
	if !config.Quiet {
		log.Printf(format, args...)
	}
}
