package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/mpdroog/mymail/smtpd/config"
	"github.com/mpdroog/mymail/smtpd/queue"
	"github.com/mpdroog/mymail/smtpd/server"
	"github.com/mpdroog/mymail/smtpd/storage"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.BoolVar(&config.Verbose, "v", false, "Verbose-mode (log more)")
	flag.Parse()

	if err := config.Load(*configPath); err != nil {
		log.Fatalf("Warning: Could not load config file: %v", err)
	}
	if config.Verbose {
		fmt.Printf("config.C=%+v\n", config.C)
	}

	st := storage.New()
	if err := st.Init(); err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Create and start SMTP server
	srv := server.New()
	srv.SetStorage(st)

	if config.C.AuthFile != "" {
		if err := srv.LoadUsers(config.C.AuthFile); err != nil {
			log.Fatalf("Warning: Could not load auth file: %v", err)
		}
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start SMTP server: %v", err)
	}

	// Start queue processor
	proc := queue.NewProcessor(st)
	proc.Start()

	daemon.SdNotify(false, daemon.SdNotifyReady)

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	daemon.SdNotify(false, daemon.SdNotifyStopping)
	log.Println("Shutting down...")
	if e := proc.Stop(); e != nil {
		log.Printf("proc.Stop e=" + e.Error())
	}
	if e := srv.Stop(); e != nil {
		log.Printf("proc.Stop e=" + e.Error())
	}
}
