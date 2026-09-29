package main

import (
	"agentmirror"
	"agentmirror/internal/lab"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	opts := lab.Options{Frontend: agentmirror.Frontend()}
	flag.StringVar(&opts.DBPath, "db", "data/agentmirror-v2.sqlite3", "SQLite data file")
	flag.StringVar(&opts.Host, "host", "0.0.0.0", "Initial legacy public bind address")
	flag.IntVar(&opts.PublicPort, "public-port", 8765, "Initial legacy public port; new databases start without public listeners")
	flag.StringVar(&opts.AdminHost, "admin-host", "127.0.0.1", "Management IPv4 bind address; configured only at startup")
	flag.IntVar(&opts.AdminPort, "admin-port", 8766, "Management port; configured only at startup")
	flag.StringVar(&opts.PublicURL, "public-url", "http://10.211.55.2:8765", "Initial advertised URL; subsequent changes are managed in the UI")
	version := flag.Bool("version", false, "Print version and exit")
	flag.Parse()
	if *version {
		fmt.Println("AgentMirror", lab.Version)
		return
	}
	app, err := lab.New(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "启动失败：", err)
		os.Exit(1)
	}
	defer app.Close()
	fmt.Print(app.Banner())
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
}
