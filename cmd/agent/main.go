package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/billcoding/proctor/internal/agent"
	"github.com/billcoding/proctor/internal/config"
	"github.com/kardianos/service"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	fs := flag.NewFlagSet(cmd, flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath(), "path to agent config json")
	serverURL := fs.String("server", "", "override server url")
	student := fs.String("student", "", "student display name")
	classroom := fs.String("classroom", "", "classroom / group")
	_ = fs.Parse(os.Args[2:])

	cfg := loadAgentConfig(*configPath)
	if *serverURL != "" {
		cfg.ServerURL = *serverURL
	}
	if *student != "" {
		cfg.StudentName = *student
	}
	if *classroom != "" {
		cfg.Classroom = *classroom
	}

	svc, rt, err := agent.NewService(cfg, *configPath)
	if err != nil {
		log.Fatalf("create service: %v", err)
	}

	switch cmd {
	case "run":
		if !service.Interactive() {
			if err := svc.Run(); err != nil {
				log.Fatal(err)
			}
			return
		}
		if err := rt.Start(); err != nil {
			log.Fatal(err)
		}
		ch := make(chan os.Signal, 1)
		notifySignals(ch)
		<-ch
		_ = rt.Stop()
	case "install", "uninstall", "start", "stop", "restart", "status":
		if err := agent.ControlService(svc, cmd); err != nil {
			log.Fatal(err)
		}
	case "init-config":
		if err := config.SaveAgent(*configPath, cfg); err != nil {
			log.Fatal(err)
		}
		fmt.Printf("wrote %s\n", *configPath)
	case "update":
		target := ""
		if fs.NArg() > 0 {
			target = fs.Arg(0)
		}
		if err := rt.RunUpdateOnce(target); err != nil {
			log.Fatalf("update: %v", err)
		}
	case "version":
		fmt.Println(agent.Version)
	case "help", "-h", "--help":
		printUsage()
	default:
		printUsage()
		os.Exit(2)
	}
}

func loadAgentConfig(path string) config.AgentConfig {
	cfg, err := config.LoadAgent(path)
	if err != nil {
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			return config.DefaultAgent()
		}
		log.Fatalf("load config: %v", err)
	}
	return cfg
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `proctor-agent — student computer audit agent

Usage:
  proctor-agent <command> [flags]

Commands:
  run           Run in foreground (or as system service entrypoint)
  install       Register as system service (macOS launchd / Linux systemd / Windows Service)
  uninstall     Remove system service
  start         Start system service
  stop          Stop system service
  restart       Restart system service
  status        Show service status
  update [ver]  Check server and apply agent self-update once (OTA; optional target version)
  init-config   Write default config json
  version       Print version

Flags:
  -config string     Config file path
  -server string     Override server URL
  -student string    Student name
  -classroom string  Classroom / group

Examples:
  sudo ./proctor-agent install -config /etc/proctor/agent.json
  sudo ./proctor-agent start
  sudo ./proctor-agent update
  sudo ./proctor-agent update 0.2.0
  ./proctor-agent run -server http://10.0.0.2:8911 -student "张三" -classroom "高一1班"
`)
}

func defaultConfigPath() string {
	candidates := []string{
		"/etc/proctor/agent.json",
		`C:\ProgramData\proctor\agent.json`,
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "./agent.json"
	}
	return filepath.Join(home, ".proctor", "agent.json")
}
