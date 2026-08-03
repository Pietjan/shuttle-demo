// Command deskline is a support desk console built with shuttle and loom.
//
//	make run        # compiles the stylesheet first
//	make run/tls    # over HTTP/2, which is what several open tabs need
//
// Agents work a shared ticket queue. What makes it worth reading as a
// quickstart is that the pieces have to work together: the queue is a live
// table over a store the browser never receives, claiming a ticket in one
// window updates it in another, and the whole console - queue, ticket,
// compose, dashboard - is *one* session and one stream.
//
// That last part is the deployment-relevant decision. Every full page load
// mints a session and opens a stream, and a browser allows about six
// connections per origin, so an app whose links reload the page runs out of
// them. Here Handler.Subtree serves every path under /desk from one
// component, links call Base.Navigate, and the address bar keeps working
// without a reload ever happening.
package main

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	cryptotls "crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"flag"
	"log"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/pietjan/shuttle"

	"github.com/pietjan/shuttle-quickstart/assets"
	"github.com/pietjan/shuttle-quickstart/internal/auth"
	"github.com/pietjan/shuttle-quickstart/internal/desk"
	"github.com/pietjan/shuttle-quickstart/internal/ui"
)

func main() {
	addr := flag.String("addr", "localhost:8080", "listen address")
	// Browsers only speak HTTP/2 over TLS, and HTTP/2 is what stops the
	// sixth open tab from silently never loading. See the comment on
	// selfSigned.
	tls := flag.Bool("tls", false, "serve over HTTPS/2 with a self-signed certificate")
	debug := flag.Bool("debug", false, "log every transport request and warn about duplicate element ids")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	store := desk.NewMemory()
	desk.Seed(store)

	// One handler for the whole console. Prefix tells shuttle where it was
	// mounted, so every URL it renders - the action endpoints, the stream -
	// is built to match; Subtree makes every path underneath this
	// component's to render rather than only the mount point itself.
	h := shuttle.New(func() shuttle.Component { return ui.NewConsole(store) })
	h.Prefix = "/desk"
	h.Subtree = true
	h.Title = "Deskline"
	h.Logger = logger
	h.Debug = *debug
	h.Shell = ui.Shell

	// Behind a reverse proxy the default charges every page load to the
	// proxy's address, which makes the whole site one bucket and the limit a
	// limit on the server rather than on a client. Left at the default here
	// because nothing is in front of it; the line is what a deployment
	// changes.
	//
	//	h.ClientIP = shuttle.ForwardedClientIP(1)

	mux := http.NewServeMux()

	// Mounted at exactly this subtree, never as a catch-all. A catch-all
	// mints a session - and a whole component tree - for every /favicon.ico
	// a crawler asks for.
	mux.Handle("/desk/", h)
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/desk/queue/", http.StatusFound)
	})

	mux.HandleFunc("GET /static/styles.css", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		http.ServeContent(w, r, "styles.css", buildTime, bytes.NewReader(assets.Stylesheet))
	})

	// Switching agent is how the two-window demo works, and it is also the
	// honest version of logging out: both close the old identity's sessions,
	// because a console already open captured that identity at mount and no
	// cookie change reaches it.
	mux.Handle("POST /signin", auth.SignIn(store, h))
	mux.Handle("POST /signout", auth.SignOut(h))

	// A session-free liveness check. Shuttle has its own health endpoint for
	// the page to poll after the stream gives up; this one is for whatever
	// is running the process.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr: *addr,
		// loom.Middleware would go here if this app rendered pages of its
		// own: it installs a per-request id counter so generated element ids
		// are deterministic. Shuttle prepares that context per render pass
		// itself, so a shuttle-only app does not need it - and adding it
		// would give every render in a session the *same* counter, which is
		// the opposite of what it is for.
		Handler:           auth.Middleware(store, mux),
		ReadHeaderTimeout: 10 * time.Second,
		// No WriteTimeout. Every connected page holds a streaming response
		// open for as long as it is on screen, and a write deadline would
		// cut it - the connection would drop, the page would reconnect, and
		// it would look like a flaky network rather than like a setting.
		IdleTimeout: 2 * time.Minute,
	}

	if *tls {
		cert, err := selfSigned()
		if err != nil {
			log.Fatal(err)
		}
		srv.TLSConfig = &cryptotls.Config{Certificates: []cryptotls.Certificate{cert}}
	}

	go func() {
		scheme := "http"
		if *tls {
			scheme = "https"
			logger.Info("serving over HTTP/2 with a self-signed certificate; your browser will not trust it")
		} else {
			logger.Warn("serving over HTTP/1.1 - a browser allows ~6 connections per origin and every open page holds one, so the sixth tab will hang. Use -tls to see the console the way it is meant to be deployed.")
		}
		logger.Info("listening", "url", scheme+"://"+*addr+"/")

		var err error
		if *tls {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	// Shutting down closes the streams, and every page notices and reloads
	// itself once something answers again. That recovery is worth exercising
	// deliberately: restart with tabs open and they come back, because the
	// state each one needs is in its URL.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	logger.Info("shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown", "err", err)
	}
}

// buildTime is the modification time reported for the embedded stylesheet,
// so conditional requests have something stable to compare against. Process
// start is close enough: the bytes cannot change while the process runs.
var buildTime = time.Now()

// selfSigned mints a throwaway certificate for localhost so the console can
// be served over HTTP/2 with no setup.
//
// This is not a convenience. Datastar streams over fetch rather than
// EventSource, so every connected page holds one of the browser's ~6
// connections per origin for as long as it is open, and that budget is
// shared across tabs. On HTTP/1.1 the sixth live page simply never loads -
// no error, no console message, the request never gets a connection to go
// out on. Over HTTP/2 every stream shares one connection and the limit stops
// existing, and browsers only speak HTTP/2 over TLS.
//
// In a deployment this is a proxy terminating TLS with HTTP/2 enabled. Here
// it is twenty lines so the failure mode can be seen rather than described.
func selfSigned() (cryptotls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return cryptotls.Certificate{}, err
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Organization: []string{"Deskline"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}

	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return cryptotls.Certificate{}, err
	}
	return cryptotls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
