// Package instance boots and manages real PostgREST processes: the process
// layer the conformance runner drives to produce the "actual" side of each
// case comparison. Each Instance is a running server built from a base
// PGRST_* config map plus a per-case overlay (internal/route.Val), talking
// to a caller-supplied db-uri, with dynamically assigned server/admin
// ports, whose readiness is proven by polling the admin /ready endpoint
// rather than assumed after a fixed sleep.
package instance

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/milmazz/postgrest-conformance/tools/oracle/internal/route"
)

// readyTimeout bounds how long Start waits for the admin /ready endpoint to
// report the instance ready before giving up.
const readyTimeout = 30 * time.Second

// readyPollInterval is how often Start polls /ready while waiting.
const readyPollInterval = 100 * time.Millisecond

// readyRequestTimeout bounds a single /ready poll request, so a hung
// connection can't itself block past readyTimeout.
const readyRequestTimeout = 2 * time.Second

// stopWait is how long Stop waits after SIGTERM before escalating to Kill.
const stopWait = 5 * time.Second

// stderrCap bounds the retained tail of a child process's stderr, in bytes.
const stderrCap = 64 * 1024

// safeUpdateQuery is the db-uri query string that preloads the safeupdate
// extension, needed by the handful of cases exercising PostgREST's
// unsafe-UPDATE/DELETE guard (HARNESS.md's cases 1387-1389).
const safeUpdateQuery = "options=-csession_preload_libraries%3Dsafeupdate"

// Instance is one running PostgREST process.
type Instance struct {
	Port      int
	AdminPort int

	cmd    *exec.Cmd
	stderr *ringBuffer

	mu      sync.Mutex
	stopped bool
}

// BuildEnv assembles the environment for a PostgREST process: base (a
// PGRST_* config map) with overlay applied on top (route.Val{Clear: true}
// deletes the key; otherwise it sets/overrides it), plus PGRST_DB_URI (with
// the safeupdate query appended when safeUpdate is set), PGRST_SERVER_PORT,
// and PGRST_ADMIN_SERVER_PORT.
//
// The result is rendered as a "k=v" slice carrying exactly those keys, plus
// PATH copied from the calling process's environment — deliberately
// nothing else. Any ambient PGRST_*/PG* variables set in the caller's shell
// are not inherited, so a case's actual behavior is never accidentally
// shaped by whatever happens to be exported in the terminal running the
// oracle.
func BuildEnv(base map[string]string, overlay map[string]route.Val, dbURI string, safeUpdate bool, port, adminPort int) []string {
	env := make(map[string]string, len(base)+len(overlay)+3)
	for k, v := range base {
		env[k] = v
	}
	for k, v := range overlay {
		if v.Clear {
			delete(env, k)
		} else {
			env[k] = v.V
		}
	}

	env["PGRST_DB_URI"] = withSafeUpdate(dbURI, safeUpdate)
	env["PGRST_SERVER_PORT"] = strconv.Itoa(port)
	env["PGRST_ADMIN_SERVER_PORT"] = strconv.Itoa(adminPort)

	out := make([]string, 0, len(env)+1)
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	if p, ok := os.LookupEnv("PATH"); ok {
		out = append(out, "PATH="+p)
	}
	return out
}

// withSafeUpdate appends safeUpdateQuery to uri when safeUpdate is set,
// using "&" instead of "?" as separator when uri already carries a query
// string.
func withSafeUpdate(uri string, safeUpdate bool) string {
	if !safeUpdate {
		return uri
	}
	sep := "?"
	if strings.Contains(uri, "?") {
		sep = "&"
	}
	return uri + sep + safeUpdateQuery
}

// freePort asks the OS for an unused TCP port on 127.0.0.1 by binding to
// port 0 and immediately releasing it. There's an inherent TOCTOU gap
// between this and the child process binding the same port, but it's the
// standard way to get an OS-assigned free port without an external port
// registry, and it's what the task calls for.
func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start boots a PostgREST process at bin with base config and overlay
// applied against dbURI (with safeupdate preloaded when safeUpdate is set),
// on two freshly allocated ports, and blocks until its admin server
// reports ready (or readyTimeout elapses).
//
// On any failure after the process has started — including a readiness
// timeout — Start stops the process before returning, and the returned
// error includes the process's stderr tail: often the only diagnostic
// available, e.g. when a bad db-schemas value in an overlay makes
// PostgREST fail to boot.
func Start(bin string, base map[string]string, overlay map[string]route.Val, dbURI string, safeUpdate bool) (*Instance, error) {
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("instance: allocate server port: %w", err)
	}
	adminPort, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("instance: allocate admin port: %w", err)
	}

	cmd := exec.Command(bin)
	cmd.Env = BuildEnv(base, overlay, dbURI, safeUpdate, port, adminPort)
	stderr := newRingBuffer(stderrCap)
	cmd.Stderr = stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("instance: start %s: %w", bin, err)
	}

	inst := &Instance{
		Port:      port,
		AdminPort: adminPort,
		cmd:       cmd,
		stderr:    stderr,
	}

	if err := waitReady(adminPort); err != nil {
		inst.Stop()
		return nil, fmt.Errorf("instance: %s did not become ready: %w\nstderr tail:\n%s", bin, err, stderr.String())
	}

	return inst, nil
}

// waitReady polls http://127.0.0.1:<adminPort>/ready every readyPollInterval
// until it returns 200, or readyTimeout elapses.
func waitReady(adminPort int) error {
	client := &http.Client{Timeout: readyRequestTimeout}
	url := fmt.Sprintf("http://127.0.0.1:%d/ready", adminPort)
	deadline := time.Now().Add(readyTimeout)

	var lastErr error
	for {
		resp, err := client.Get(url)
		if err != nil {
			lastErr = err
		} else {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("unexpected status %s", resp.Status)
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s: %w", readyTimeout, lastErr)
		}
		time.Sleep(readyPollInterval)
	}
}

// Stop sends SIGTERM to the process, waits up to stopWait for it to exit,
// and escalates to SIGKILL if it hasn't. Safe to call more than once
// (including on an Instance returned from a failed Start), and safe to
// call on an Instance whose process has already exited on its own.
func (i *Instance) Stop() {
	i.mu.Lock()
	if i.stopped || i.cmd == nil || i.cmd.Process == nil {
		i.mu.Unlock()
		return
	}
	i.stopped = true
	proc := i.cmd.Process
	cmd := i.cmd
	i.mu.Unlock()

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	_ = proc.Signal(syscall.SIGTERM)

	select {
	case <-done:
		return
	case <-time.After(stopWait):
	}

	_ = proc.Kill()
	<-done
}

// ringBuffer is an io.Writer that retains only the last max bytes written
// to it, used to capture a bounded tail of a child process's stderr without
// letting a noisy or runaway process grow memory unbounded.
type ringBuffer struct {
	mu  sync.Mutex
	max int
	buf []byte
}

func newRingBuffer(max int) *ringBuffer {
	return &ringBuffer{max: max}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.max {
		r.buf = r.buf[len(r.buf)-r.max:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}
