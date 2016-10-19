package main

import (
	"io"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jpillora/opts"
)

//compiler-settable version
var VERSION = "0.0.0-src"

//define cli
var config = struct {
	SocketAddr string `help:"path to unix socket file to listen on"`
	TCPAddr    string `help:"remote tcp socket address to forward to"`
	Quiet      bool   `help:"suppress connection logs"`
}{
	SocketAddr: "/var/run/fwd.sock",
	TCPAddr:    "127.0.0.1:22",
	Quiet:      false,
}

var (
	SIGINT  = os.Interrupt
	SIGKILL = os.Kill
	//allows compilation under windows,
	//even though it cannot send USR signals
	SIGUSR1 = syscall.Signal(0xa)
	SIGUSR2 = syscall.Signal(0xc)
	SIGTERM = syscall.Signal(0xf)
)

func main() {
	//init and parse cli
	o := opts.New(&config)
	o.LineWidth = 60
	o.DocAfter("options", "signal",
		"\nthe sockfwd process will accept a:\n"+
			"  USR1 signal to print uptime and connection stats\n"+
			"  USR2 signal to toggle connection logging (--quiet)\n")
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
		signal.Notify(c)
		for sig := range c {
			switch sig {
			case SIGINT, SIGTERM, SIGKILL:
				l.Close()
				os.Remove(config.SocketAddr)
				logf("closed listener and removed socket")
				os.Exit(0)
			case SIGUSR1:
				mem := runtime.MemStats{}
				runtime.ReadMemStats(&mem)
				logf("stats:\n"+
					"  %s, uptime: %s\n"+
					"  goroutines: %d, mem-alloc: %d\n"+
					"  connections open: %d total: %d",
					runtime.Version(), time.Now().Sub(uptime),
					runtime.NumGoroutine(), mem.Alloc,
					atomic.LoadInt64(&current), atomic.LoadUint64(&total))
			case SIGUSR2:
				//toggle logging with USR2 signal
				config.Quiet = !config.Quiet
				logf("connection logging: %v", config.Quiet)
			}
		}
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
var uptime = time.Now()
var total uint64
var current int64

//pool of buffers (default to io.Copy buffer size)
var pool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 32*1024)
	},
}

func fwd(uconn net.Conn) {
	tconn, err := net.Dial("tcp", config.TCPAddr)
	if err != nil {
		log.Printf("tcp dial failed: %s", err)
		uconn.Close()
		return
	}
	//stats
	atomic.AddUint64(&total, 1)
	atomic.AddInt64(&current, 1)
	//optional log
	if !config.Quiet {
		logf("connection #%d (%d open)", atomic.LoadUint64(&total), atomic.LoadInt64(&current))
	}
	//pipe!
	go func() {
		ubuff := pool.Get().([]byte)
		io.CopyBuffer(uconn, tconn, ubuff)
		pool.Put(ubuff)
		uconn.Close()
		//stats
		atomic.AddInt64(&current, -1)
	}()
	go func() {
		tbuff := pool.Get().([]byte)
		io.CopyBuffer(tconn, uconn, tbuff)
		pool.Put(tbuff)
		tconn.Close()
	}()
}

func logf(format string, args ...interface{}) {
	log.Printf(format, args...)
}
