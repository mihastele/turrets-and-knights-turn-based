// Lane Siege — a head-to-head lane siege game.
//
// The game client is embedded into the binary, so a single static binary is
// all you need to host it:
//
//	go run ./cmd/lanesiege
//	docker build -t lanesiege . && docker run -p 8180:8180 lanesiege
//
// Flags / environment:
//
//	-addr        listen address (default: ":8180", or ":"+$PORT when PORT is set,
//	             e.g. on Fly.io / Render / Heroku-style platforms)
//	-web         serve the client from this directory instead of the embedded
//	             assets (useful for editing the client without recompiling)
//	-admin-token bearer token for the coupon admin API (or $LANESIEGE_ADMIN_TOKEN);
//	             empty disables the admin API
//	-coupons     path of the coupon store JSON file (or $LANESIEGE_COUPONS_FILE);
//	             default "coupons.json"; the file is created on first write
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"

	lanesiege "lanesiege"
	"lanesiege/internal/coupons"
	"lanesiege/internal/server"
)

const defaultPort = "8180"

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv reads KEY=VALUE pairs from path into the process environment
// without overriding variables that are already set. Missing files are ignored.
func loadDotEnv(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && ((val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'')) {
			val = val[1 : len(val)-1]
		}
		if _, exists := os.LookupEnv(key); !exists {
			_ = os.Setenv(key, val)
		}
	}
	return scanner.Err()
}

func resolveListenAddr(addrFlag string) string {
	if addrFlag != "" {
		return addrFlag
	}
	return ":" + envOr("PORT", defaultPort)
}

func main() {
	if err := loadDotEnv(".env"); err != nil {
		log.Printf("warning: failed to load .env: %v", err)
	}

	addr := flag.String("addr", "", "listen address (overrides $PORT; default :8180)")
	web := flag.String("web", "", "serve client from this directory instead of the embedded assets")
	adminToken := flag.String("admin-token", os.Getenv("LANESIEGE_ADMIN_TOKEN"), "bearer token for the coupon admin API (env LANESIEGE_ADMIN_TOKEN); empty disables it")
	couponsPath := flag.String("coupons", envOr("LANESIEGE_COUPONS_FILE", "coupons.json"), "path of the coupon store file (env LANESIEGE_COUPONS_FILE)")
	flag.Parse()

	listen := resolveListenAddr(*addr)

	var webFS fs.FS
	if *web != "" {
		webFS = os.DirFS(*web)
		log.Printf("serving client from disk: %s", *web)
	} else {
		sub, err := fs.Sub(lanesiege.Web, "web")
		if err != nil {
			log.Fatalf("embedded assets: %v", err)
		}
		webFS = sub
	}

	couponStore, err := coupons.Load(*couponsPath)
	if err != nil {
		log.Fatalf("loading coupons from %s: %v", *couponsPath, err)
	}
	log.Printf("coupon store: %s", *couponsPath)
	if *adminToken == "" {
		log.Printf("coupon admin API disabled (no -admin-token / LANESIEGE_ADMIN_TOKEN)")
	} else {
		log.Printf("coupon admin API enabled at /api/admin/coupons")
	}

	srv := server.NewWithOptions(webFS, server.Options{
		Coupons:    couponStore,
		AdminToken: *adminToken,
	})

	// Graceful shutdown on Ctrl+C / SIGTERM (docker stop, systemd stop, …).
	go func() {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		<-ctx.Done()
		log.Printf("shutting down")
		srv.Close()
	}()

	log.Printf("Lane Siege listening on http://0.0.0.0%s", listen)
	log.Printf("Open two browser tabs (or two devices) and share a room code.")
	if err := srv.ListenAndServe(listen); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
