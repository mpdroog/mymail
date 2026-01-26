package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
)

func main() {
	var (
		addr          = flag.String("addr", ":1143", "IMAP server address")
		maildir       = flag.String("maildir", "./maildir", "Path to mail storage directory")
		usersFile     = flag.String("users", "./users.txt", "Path to users file (username:password per line)")
		whitelistFile = flag.String("whitelist", "./whitelist.txt", "Path to sender whitelist file")
		insecure      = flag.Bool("insecure", true, "Allow authentication without TLS")
	)
	flag.Parse()

	users, err := NewUserStore(*usersFile)
	if err != nil {
		log.Fatalf("Failed to load users: %v", err)
	}

	storage, err := NewStorage(*maildir, *whitelistFile)
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
		InsecureAuth: *insecure,
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
			if err := storage.ReloadWhitelist(); err != nil {
				log.Printf("Failed to reload whitelist: %v", err)
			}
			log.Println("Configuration reloaded")
		}
	}()

	log.Printf("Starting IMAP server on %s", *addr)
	log.Printf("Mail directory: %s", *maildir)
	log.Printf("Users file: %s", *usersFile)
	log.Printf("Whitelist file: %s", *whitelistFile)
	if *insecure {
		log.Println("WARNING: Insecure auth enabled (no TLS required)")
	}

	if err := imapSrv.ListenAndServe(*addr); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}
