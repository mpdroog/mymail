package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/mpdroog/mymail/imapd/config"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration file")
	flag.BoolVar(&config.Verbose, "v", false, "Verbose-mode (log more)")
	flag.Parse()

	if err := config.Load(*configPath); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	if config.Verbose {
		fmt.Printf("config.C=%+v\n", config.C)
	}

	users, err := NewUserStore(config.C.AuthFile)
	if err != nil {
		log.Fatalf("Failed to load users: %v", err)
	}

	storage, err := NewStorage(config.C.MailDir, config.C.Domain)
	if err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	srv := NewServer(users, storage)

	caps := make(imap.CapSet)
	caps[imap.CapIMAP4rev1] = struct{}{}

	opts := &imapserver.Options{
		NewSession: func(conn *imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return srv.NewSession(), nil, nil
		},
		Caps:         caps,
		InsecureAuth: config.C.InsecureAuth,
	}
	if config.Verbose {
		opts.DebugWriter = os.Stdout
	}

	imapSrv := imapserver.New(opts)

	// Handle SIGHUP for config reload
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGHUP)
	go func() {
		for range sigs {
			log.Println("Reloading configuration...")
			if err := users.Reload(); err != nil {
				log.Printf("Failed to reload users: %v", err)
			}
			log.Println("Configuration reloaded")
		}
	}()

	if config.C.InsecureAuth {
		log.Println("WARNING: Insecure auth enabled (no TLS required)")
	}

	if err := imapSrv.ListenAndServe(config.C.ListenAddr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
