package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/mpdroog/mymail/smtpd/config"
	"github.com/mpdroog/mymail/smtpd/queue"
	"github.com/mpdroog/mymail/smtpd/server"
	"github.com/mpdroog/mymail/smtpd/storage"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to configuration file")
	genConfig := flag.Bool("genconfig", false, "Generate default configuration file")
	flag.Parse()

	if *genConfig {
		generateDefaultConfig()
		return
	}

	// Load configuration
	if err := config.Load(*configPath); err != nil {
		log.Printf("Warning: Could not load config file: %v", err)
		log.Println("Using default configuration")
		config.C = config.Default()
	}

	// Initialize storage
	st := storage.New()
	if err := st.Init(); err != nil {
		log.Fatalf("Failed to initialize storage: %v", err)
	}

	// Create and start SMTP server
	srv := server.New()
	srv.SetStorage(st)

	if config.C.AuthFile != "" {
		if err := srv.LoadUsers(config.C.AuthFile); err != nil {
			log.Printf("Warning: Could not load auth file: %v", err)
		}
	}

	if err := srv.Start(); err != nil {
		log.Fatalf("Failed to start SMTP server: %v", err)
	}

	// Start queue processor
	proc := queue.NewProcessor(st)
	proc.Start()

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	proc.Stop()
	srv.Stop()
}

func generateDefaultConfig() {
	cfg := config.Default()

	f, err := os.Create("config.json")
	if err != nil {
		log.Fatalf("Failed to create config file: %v", err)
	}
	defer f.Close()

	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(cfg); err != nil {
		log.Fatalf("Failed to write config: %v", err)
	}

	log.Println("Generated default config.json")
}
