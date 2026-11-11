// Command stackit_mock runs the STACKIT API mock and, by default, the TUI in one process.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/morks/stackit_mock/internal/httpx"
	"github.com/morks/stackit_mock/internal/lifecycle"
	"github.com/morks/stackit_mock/internal/server"
	"github.com/morks/stackit_mock/internal/services"
	"github.com/morks/stackit_mock/internal/store"
	"github.com/morks/stackit_mock/internal/tui"
)

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func main() {
	addr := flag.String("addr", env("MOCK_ADDR", ":9001"), "listen address (MOCK_ADDR)")
	base := flag.String("base-url", env("MOCK_BASE_URL", ""), "external base URL, e.g. https://mock.lab (MOCK_BASE_URL); default derived from request/X-Forwarded-*")
	snap := flag.String("snapshot", env("MOCK_SNAPSHOT", ""), "JSON snapshot file; empty = in-memory only (MOCK_SNAPSHOT)")
	delay := flag.Duration("delay", func() time.Duration { d, _ := time.ParseDuration(env("MOCK_DELAY", "0s")); return d }(), "duration of async state transitions, 0 = instant (MOCK_DELAY); e.g. 2s to watch CREATING→ACTIVE in the TUI")
	strict := flag.Bool("strict-auth", env("MOCK_STRICT_AUTH", "") == "1", "require Bearer token on service routes (MOCK_STRICT_AUTH=1)")
	headless := flag.Bool("headless", false, "run only the server, no TUI (logs requests to stderr)")
	tuiOnly := flag.Bool("tui-only", false, "run only the TUI against a running mock (see -url)")
	adminURL := flag.String("url", env("MOCK_ADMIN_URL", ""), "admin API URL for -tui-only (MOCK_ADMIN_URL), default http://localhost:9001/_admin")
	quiet := flag.Bool("quiet", false, "disable request logging in headless mode")
	flag.Parse()

	if *headless && *tuiOnly {
		fatal(errors.New("-headless and -tui-only are mutually exclusive"))
	}
	if *tuiOnly {
		url := *adminURL
		if url == "" {
			url = "http://localhost:9001/_admin"
		}
		fatal(runTUI(tui.New(url)))
		return
	}

	st := store.New()
	if *snap != "" {
		if err := st.Load(*snap); err != nil {
			fatal(fmt.Errorf("load snapshot: %w", err))
		}
	}
	eng := lifecycle.New(st, *delay)
	eng.Resume()
	d := &httpx.Deps{Store: st, Engine: eng, BaseURL: *base}
	server.Registrars = services.All()
	// Request logs would corrupt the TUI screen, so they only go to stderr in headless mode.
	withTUI := !*headless
	if withTUI {
		log.SetOutput(io.Discard)
	}
	h, _ := server.New(d, server.Options{StrictAuth: *strict, Quiet: *quiet || withTUI})

	// Listen first so a busy port fails before the TUI takes over the terminal.
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if *snap != "" {
		go func() {
			t := time.NewTicker(2 * time.Second)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					if err := st.Save(*snap); err != nil {
						log.Printf("snapshot: %v", err)
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	if withTUI {
		fatalIf(runTUI(tui.New("http://mock.local/_admin").WithShownURL(shownURL(ln)).WithClient(&http.Client{Transport: inProcess{h}, Timeout: 3 * time.Second})), srv, st, *snap)
	} else {
		log.Printf("stackit mock listening on %s (delay=%s, snapshot=%q, strict=%t)", ln.Addr(), *delay, *snap, *strict)
		select {
		case <-ctx.Done():
		case err := <-serveErr:
			if !errors.Is(err, http.ErrServerClosed) {
				fatal(err)
			}
		}
	}
	shutdown(srv, st, *snap)
}

func runTUI(m tui.Model) error {
	_, err := tea.NewProgram(m, tea.WithAltScreen()).Run()
	return err
}

// inProcess serves requests straight from the handler. The combined mode must
// not go through the network: another process (e.g. an ssh port forward) may
// already own 127.0.0.1:<port> while the mock listens on the IPv6 wildcard.
type inProcess struct{ h http.Handler }

func (t inProcess) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	t.h.ServeHTTP(rec, r)
	return rec.Result(), nil
}

func shutdown(srv *http.Server, st *store.Store, snap string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	if snap != "" {
		if err := st.Save(snap); err != nil {
			fmt.Fprintln(os.Stderr, "snapshot:", err)
		}
	}
}

func fatalIf(err error, srv *http.Server, st *store.Store, snap string) {
	if err != nil {
		shutdown(srv, st, snap)
		fatal(err)
	}
}

func fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "stackit_mock:", err)
		os.Exit(1)
	}
}

// shownURL is the URL clients should use for the mock, shown in the TUI header.
func shownURL(ln net.Listener) string {
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return "http://localhost:" + port
}
