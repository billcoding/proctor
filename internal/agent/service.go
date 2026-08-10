package agent

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"github.com/billcoding/proctor/internal/config"
	"github.com/kardianos/service"
)

// program adapts Runtime to kardianos/service.Interface.
type program struct {
	runtime *Runtime
}

func (p *program) Start(s service.Service) error {
	return p.runtime.Start()
}

func (p *program) Stop(s service.Service) error {
	return p.runtime.Stop()
}

// ServiceConfig builds OS-specific service metadata.
func ServiceConfig(exePath string, configPath string) *service.Config {
	args := []string{"run"}
	if configPath != "" {
		args = append(args, "-config", configPath)
	}

	cfg := &service.Config{
		Name:        "proctor-agent",
		DisplayName: "Proctor Student Agent",
		Description: "Student computer audit agent for process/network/resource supervision",
		Arguments:   args,
		Option:      service.KeyValue{},
	}

	switch runtime.GOOS {
	case "linux":
		cfg.Option["SystemdScript"] = linuxSystemdScript()
		cfg.Option["Restart"] = "always"
		cfg.Dependencies = []string{
			"Requires=network.target",
			"After=network-online.target",
		}
	case "darwin":
		// launchd KeepAlive + RunAtLoad for classroom machines.
		cfg.Option["KeepAlive"] = true
		cfg.Option["RunAtLoad"] = true
		cfg.Option["UserService"] = false
		cfg.Option["SessionCreate"] = true
		if exePath != "" {
			cfg.Executable = exePath
		}
	case "windows":
		cfg.Option["OnFailure"] = "restart"
		cfg.Option["OnFailureDelayDuration"] = "5s"
		cfg.Option["OnFailureResetPeriod"] = 10
	}
	return cfg
}

func NewService(cfg config.AgentConfig, configPath string) (service.Service, *Runtime, error) {
	rt, err := NewRuntime(cfg, configPath)
	if err != nil {
		return nil, nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		exe = ""
	} else if resolved, err2 := filepath.EvalSymlinks(exe); err2 == nil {
		exe = resolved
	}
	svcCfg := ServiceConfig(exe, configPath)
	prg := &program{runtime: rt}
	svc, err := service.New(prg, svcCfg)
	if err != nil {
		return nil, nil, err
	}
	return svc, rt, nil
}

// ControlService installs/starts/stops/uninstalls the OS service.
func ControlService(svc service.Service, action string) error {
	switch action {
	case "install":
		if err := svc.Install(); err != nil {
			return fmt.Errorf("install service: %w", err)
		}
		log.Printf("service installed (%s)", runtime.GOOS)
		return nil
	case "uninstall":
		_ = svc.Stop()
		if err := svc.Uninstall(); err != nil {
			return fmt.Errorf("uninstall service: %w", err)
		}
		log.Printf("service uninstalled")
		return nil
	case "start":
		if err := svc.Start(); err != nil {
			return fmt.Errorf("start service: %w", err)
		}
		log.Printf("service started")
		return nil
	case "stop":
		if err := svc.Stop(); err != nil {
			return fmt.Errorf("stop service: %w", err)
		}
		log.Printf("service stopped")
		return nil
	case "restart":
		if err := service.Control(svc, "restart"); err != nil {
			return fmt.Errorf("restart service: %w", err)
		}
		log.Printf("service restarted")
		return nil
	case "status":
		st, err := svc.Status()
		if err != nil {
			return err
		}
		switch st {
		case service.StatusRunning:
			fmt.Println("running")
		case service.StatusStopped:
			fmt.Println("stopped")
		default:
			fmt.Println("unknown")
		}
		return nil
	default:
		return fmt.Errorf("unknown service action: %s", action)
	}
}

func linuxSystemdScript() string {
	return `[Unit]
Description={{.Description}}
ConditionFileIsExecutable={{.Path|cmdEscape}}
{{range $i, $dep := .Dependencies}}
{{$dep}}{{end}}

[Service]
StartLimitInterval=5
StartLimitBurst=10
ExecStart={{.Path|cmdEscape}}{{range .Arguments}} {{.|cmd}}{{end}}
Restart=always
RestartSec=3
WorkingDirectory={{.WorkingDirectory|cmdEscape}}
EnvironmentFile=-/etc/proctor/agent.env

[Install]
WantedBy=multi-user.target
`
}
