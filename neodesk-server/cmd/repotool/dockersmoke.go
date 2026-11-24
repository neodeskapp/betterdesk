package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// dockerSmoke is "docker-smoke", the local gate's runtime test of the
// image: it starts the local image (NEODESK_LOCAL_IMAGE, default
// neodesk:local) in a container of its own with new named volumes,
// waits until the API and the console answer their health addresses,
// signs in as the admin, runs the server's own "check" inside the
// container, restarts the container and checks that the API key stayed
// the same and that Docker reports the container healthy. It never
// touches the host's install or the Compose containers of the project;
// the container and volumes are removed afterwards unless --keep is given.
func dockerSmoke(root string, args []string, stdout, stderr io.Writer) int {
	opts, err := parseSmokeArgs(args)
	if err == nil && opts.help {
		fmt.Fprintln(stdout, "Usage: go run ./cmd/repotool docker-smoke [--database-type sqlite|postgres] [--database-url URL] [--keep]")
		return 0
	}
	if err == nil {
		s := &smoke{
			root:   root,
			stdout: stdout,
			stderr: stderr,
			image:  os.Getenv("NEODESK_LOCAL_IMAGE"),
			docker: runDocker(root),
			client: smokeClient(),
			sleep:  time.Sleep,
			suffix: fmt.Sprintf("%d-%d", os.Getpid(), time.Now().UnixMilli()),
		}
		err = s.run(opts)
	}
	if err != nil {
		fmt.Fprintf(stderr, "FAIL local Docker smoke: %v\n", err)
		return 1
	}
	return 0
}

// smokePassword is the admin password of the throwaway container; it is
// used for nothing else.
const smokePassword = "NeoDesk-Local-Gate-Only-2026!"

const (
	smokeRequestTimeout = 5 * time.Second
	smokeStartupTimeout = 120 * time.Second
	smokeHealthyTimeout = 60 * time.Second
)

type smokeOptions struct {
	databaseURL  string
	databaseType string
	keep         bool
	help         bool
}

func parseSmokeArgs(args []string) (smokeOptions, error) {
	o := smokeOptions{databaseType: "sqlite"}
	next := func(i *int) string {
		*i++
		if *i < len(args) {
			return args[*i]
		}
		return ""
	}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--database-url":
			o.databaseURL = next(&i)
		case "--database-type":
			if o.databaseType = next(&i); o.databaseType == "" {
				o.databaseType = "sqlite"
			}
		case "--keep":
			o.keep = true
		case "--help", "-h":
			o.help = true
		default:
			return o, fmt.Errorf("Unknown option: %s", args[i])
		}
	}
	if o.databaseType != "sqlite" && o.databaseType != "postgres" {
		return o, errors.New("--database-type must be sqlite or postgres")
	}
	if o.databaseType == "postgres" && o.databaseURL == "" {
		return o, errors.New("--database-url is required for postgres smoke tests")
	}
	return o, nil
}

// dockerResult is what a docker command printed and returned.
type dockerResult struct {
	stdout, stderr string
	code           int
	err            error // the command did not run
}

// dockerRunner runs docker with the arguments.
type dockerRunner func(args ...string) dockerResult

func runDocker(root string) dockerRunner {
	return func(args ...string) dockerResult {
		cmd := exec.Command("docker", args...)
		cmd.Dir = root
		var out, errOut bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errOut
		err := cmd.Run()
		r := dockerResult{stdout: out.String(), stderr: errOut.String()}
		var exitErr *exec.ExitError
		switch {
		case errors.As(err, &exitErr):
			r.code = exitErr.ExitCode()
		case err != nil:
			r.err = err
		}
		return r
	}
}

// smokeClient does not follow redirects: the sign-in reads them itself.
func smokeClient() *http.Client {
	return &http.Client{
		Timeout:       smokeRequestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

type smoke struct {
	root           string
	stdout, stderr io.Writer
	image          string
	docker         dockerRunner
	client         *http.Client
	sleep          func(time.Duration)
	suffix         string
}

// mustDocker runs docker and fails when the command fails.
func (s *smoke) mustDocker(args ...string) (dockerResult, error) {
	r := s.docker(args...)
	if r.err != nil || r.code != 0 {
		detail := ""
		switch {
		case r.err != nil:
			detail = r.err.Error()
		case strings.TrimSpace(r.stderr) != "":
			detail = strings.TrimSpace(r.stderr)
		case strings.TrimSpace(r.stdout) != "":
			detail = strings.TrimSpace(r.stdout)
		default:
			detail = fmt.Sprintf("exit code %d", r.code)
		}
		return r, fmt.Errorf("docker %s failed: %s", strings.Join(args, " "), detail)
	}
	return r, nil
}

func (s *smoke) run(o smokeOptions) (err error) {
	image := s.image
	if image == "" {
		image = "neodesk:local"
	}
	container := "neodesk-local-gate-" + s.suffix
	dataVolume := "neodesk-local-gate-data-" + s.suffix
	consoleVolume := "neodesk-local-gate-console-" + s.suffix

	environment := []string{
		"NODE_ENV=production",
		"CONSOLE_PORT=5000",
		"HOST=0.0.0.0",
		"API_HOST=0.0.0.0",
		"API_ENABLED=false",
		"SERVER_BACKEND=neodesk",
		"NEODESK_API_URL=http://127.0.0.1:21121/api",
		"HBBS_API_URL=http://127.0.0.1:21121/api",
		"RUSTDESK_PATH=/opt/neodesk/data",
		"DATA_DIR=/app/data",
		"DB_PATH=/opt/neodesk/data/db_v2.sqlite3",
		"AUTH_DB_PATH=/app/data/auth.db",
		"ADMIN_PASSWORD=" + smokePassword,
		"INIT_ADMIN_USER=admin",
		"INIT_ADMIN_PASS=" + smokePassword,
		"DEFAULT_ADMIN_USERNAME=admin",
		"DEFAULT_ADMIN_PASSWORD=" + smokePassword,
		"DOCKER=true",
		"AGENT_BUILD_WORKER=off",
		"NEODESK_GITHUB_TOKEN=",
		"PUID=10001",
		"PGID=10001",
		"ENROLLMENT_MODE=managed",
	}
	if o.databaseType == "postgres" {
		environment = append(environment, "DB_TYPE=postgres", "DATABASE_URL="+o.databaseURL, "DB_URL="+o.databaseURL)
	} else {
		environment = append(environment, "DB_TYPE=sqlite")
	}

	defer func() {
		if err != nil {
			logs := s.docker("logs", "--tail", "200", container)
			fmt.Fprint(s.stderr, logs.stdout)
			fmt.Fprint(s.stderr, logs.stderr)
		}
		if !o.keep {
			s.docker("rm", "--force", container)
			s.docker("volume", "rm", "--force", dataVolume)
			s.docker("volume", "rm", "--force", consoleVolume)
		}
	}()

	if _, err := s.mustDocker("volume", "create", dataVolume); err != nil {
		return err
	}
	if _, err := s.mustDocker("volume", "create", consoleVolume); err != nil {
		return err
	}
	runArgs := []string{
		"run", "--detach",
		"--name", container,
		"--label", "com.neodesk.local-gate=true",
		"--publish", "127.0.0.1::5000",
		"--publish", "127.0.0.1::21121",
		"--volume", dataVolume + ":/opt/neodesk/data",
		"--volume", consoleVolume + ":/app/data",
		"--add-host", "host.docker.internal:host-gateway",
	}
	for _, v := range environment {
		runArgs = append(runArgs, "--env", v)
	}
	if _, err := s.mustDocker(append(runArgs, image)...); err != nil {
		return err
	}

	apiURL, panelURL, err := s.urls(container)
	if err != nil {
		return err
	}
	if err := s.waitForHealth(apiURL, panelURL); err != nil {
		return err
	}
	if err := s.login(panelURL); err != nil {
		return err
	}
	firstKey, err := s.apiKey(container)
	if err != nil {
		return err
	}
	if firstKey == "" {
		return errors.New("container did not create an API key")
	}

	// The server's own protocol check, inside the container
	protocol := s.docker("exec", "-u", "neodesk", container, "/usr/local/bin/neodesk-server", "check",
		"--api", "http://127.0.0.1:21121/api/health",
		"--panel", "http://127.0.0.1:5000/health",
		"--port", "127.0.0.1:21121")
	fmt.Fprint(s.stdout, protocol.stdout)
	fmt.Fprint(s.stderr, protocol.stderr)
	if protocol.err != nil || protocol.code != 0 {
		code := "null"
		if protocol.err == nil {
			code = strconv.Itoa(protocol.code)
		}
		return fmt.Errorf("neodesk-server check failed with exit code %s", code)
	}

	if _, err := s.mustDocker("restart", container); err != nil {
		return err
	}
	if apiURL, panelURL, err = s.urls(container); err != nil {
		return err
	}
	if err := s.waitForHealth(apiURL, panelURL); err != nil {
		return err
	}
	if err := s.login(panelURL); err != nil {
		return err
	}
	secondKey, err := s.apiKey(container)
	if err != nil {
		return err
	}
	if firstKey != secondKey {
		return errors.New("API key changed after a container restart")
	}
	if err := s.waitForContainerHealthy(container); err != nil {
		return err
	}
	fmt.Fprintf(s.stdout, "PASS local Docker smoke (%s) %s %s\n", o.databaseType, apiURL, panelURL)
	return nil
}

func (s *smoke) apiKey(container string) (string, error) {
	r, err := s.mustDocker("exec", container, "sh", "-c", "cat /opt/neodesk/data/.api_key")
	return strings.TrimSpace(r.stdout), err
}

// urls are the API's and the console's addresses on the ports Docker
// published.
func (s *smoke) urls(container string) (apiURL, panelURL string, err error) {
	panelPort, err := s.mappedPort(container, 5000)
	if err != nil {
		return "", "", err
	}
	apiPort, err := s.mappedPort(container, 21121)
	if err != nil {
		return "", "", err
	}
	return "http://127.0.0.1:" + apiPort, "http://127.0.0.1:" + panelPort, nil
}

func (s *smoke) mappedPort(container string, port int) (string, error) {
	r, err := s.mustDocker("port", container, fmt.Sprintf("%d/tcp", port))
	if err != nil {
		return "", err
	}
	m := reMappedPort.FindStringSubmatch(r.stdout)
	if m == nil {
		return "", fmt.Errorf("could not determine mapped port %d for %s", port, container)
	}
	n, _ := strconv.Atoi(m[1])
	return strconv.Itoa(n), nil
}

// smokeResponse is an answer as the checks read it.
type smokeResponse struct {
	status int
	header http.Header
	body   string
}

func (s *smoke) request(method, target string, header map[string]string, body string) (smokeResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), smokeRequestTimeout)
	defer cancel()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return smokeResponse{}, err
	}
	for k, v := range header {
		req.Header.Set(k, v)
	}
	res, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return smokeResponse{}, errors.New("request timed out")
		}
		return smokeResponse{}, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return smokeResponse{}, err
	}
	return smokeResponse{status: res.StatusCode, header: res.Header, body: string(data)}, nil
}

func ok2xx(status int) bool { return status >= 200 && status < 300 }

func (s *smoke) waitForHealth(apiURL, panelURL string) error {
	deadline := time.Now().Add(smokeStartupTimeout)
	last := "services have not started"
	for time.Now().Before(deadline) {
		api, apiErr := s.request(http.MethodGet, apiURL+"/api/health", nil, "")
		panel, panelErr := s.request(http.MethodGet, panelURL+"/health", nil, "")
		switch {
		case apiErr != nil:
			last = apiErr.Error()
		case panelErr != nil:
			last = panelErr.Error()
		case ok2xx(api.status) && ok2xx(panel.status):
			return nil
		default:
			last = fmt.Sprintf("API %d, panel %d", api.status, panel.status)
		}
		s.sleep(time.Second)
	}
	return fmt.Errorf("services did not become healthy: %s", last)
}

func (s *smoke) waitForContainerHealthy(container string) error {
	deadline := time.Now().Add(smokeHealthyTimeout)
	status := "unknown"
	for time.Now().Before(deadline) {
		r, err := s.mustDocker("inspect", "--format", "{{.State.Health.Status}}", container)
		if err != nil {
			return err
		}
		switch status = strings.TrimSpace(r.stdout); status {
		case "healthy":
			return nil
		case "unhealthy":
			return errors.New("container health status is unhealthy")
		}
		s.sleep(time.Second)
	}
	if status == "" {
		status = "unknown"
	}
	return fmt.Errorf("container health status remained %s", status)
}

// firstCookie is the name=value part of every Set-Cookie, joined for a
// Cookie header.
func firstCookie(h http.Header) string {
	var parts []string
	for _, c := range h.Values("Set-Cookie") {
		if v, _, _ := strings.Cut(c, ";"); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, "; ")
}

var csrfPatterns = []*regexp.Regexp{
	regexp.MustCompile(`csrfToken:` + jsSpace + `*'([^']+)'`),
	regexp.MustCompile(`csrfToken:` + jsSpace + `*"([^"]+)"`),
	regexp.MustCompile(`(?i)name=["']_csrf["'][^>]+value=["']([^"']+)["']`),
}

// getCsrfToken is the CSRF token a sign-in page carries in its HTML.
func getCsrfToken(html string) string {
	for _, re := range csrfPatterns {
		if m := re.FindStringSubmatch(html); m != nil {
			return m[1]
		}
	}
	return ""
}

// login signs in as the admin and checks the session.
func (s *smoke) login(panelURL string) error {
	// The sign-in page answers 200 without a session: directly, or after one
	// redirect to the console under /app
	page, err := s.request(http.MethodGet, panelURL+"/login", nil, "")
	if err != nil {
		return err
	}
	if page.status == http.StatusFound && page.header.Get("Location") != "" {
		base, _ := url.Parse(panelURL)
		next, err := base.Parse(page.header.Get("Location"))
		if err != nil {
			return err
		}
		if page, err = s.request(http.MethodGet, next.String(), nil, ""); err != nil {
			return err
		}
	}
	if !ok2xx(page.status) {
		return fmt.Errorf("login page returned HTTP %d", page.status)
	}

	// A page may carry the CSRF token in its HTML; the console asks the server for it
	csrf := getCsrfToken(page.body)
	cookie := firstCookie(page.header)
	if csrf == "" {
		boot, err := s.request(http.MethodGet, panelURL+"/api/auth/login-page", map[string]string{"Accept": "application/json"}, "")
		if err != nil {
			return err
		}
		var payload struct {
			CSRFToken any `json:"csrfToken"`
		}
		if json.Unmarshal([]byte(boot.body), &payload) == nil {
			if token, ok := payload.CSRFToken.(string); ok {
				csrf = token
			}
		}
		cookie = firstCookie(boot.header)
	}
	if csrf == "" {
		return errors.New("login page did not expose a CSRF token")
	}
	body := `{"username":"admin","password":"` + smokePassword + `"}`
	res, err := s.request(http.MethodPost, panelURL+"/api/auth/login", map[string]string{
		"Content-Type": "application/json",
		"Cookie":       cookie,
		"X-CSRF-Token": csrf,
	}, body)
	if err != nil {
		return err
	}
	if !ok2xx(res.status) {
		return fmt.Errorf("login returned HTTP %d: %s", res.status, res.body)
	}
	var login struct {
		Success any `json:"success"`
	}
	if err := json.Unmarshal([]byte(res.body), &login); err != nil {
		return fmt.Errorf("login returned invalid JSON: %s", res.body)
	}
	if login.Success != true {
		return fmt.Errorf("login was rejected: %s", res.body)
	}

	var cookies []string
	for _, c := range []string{cookie, firstCookie(res.header)} {
		if c != "" {
			cookies = append(cookies, c)
		}
	}
	verify, err := s.request(http.MethodGet, panelURL+"/api/auth/verify", map[string]string{"Cookie": strings.Join(cookies, "; ")}, "")
	if err != nil {
		return err
	}
	if !ok2xx(verify.status) {
		return fmt.Errorf("session verification returned HTTP %d", verify.status)
	}
	var session struct {
		Success any `json:"success"`
		User    any `json:"user"`
	}
	if err := json.Unmarshal([]byte(verify.body), &session); err != nil {
		return fmt.Errorf("session verification returned invalid JSON: %s", verify.body)
	}
	if session.Success != true || !jsTruthyJSON(session.User) {
		return fmt.Errorf("session verification failed: %s", verify.body)
	}
	return nil
}

// jsTruthyJSON is JavaScript's truthiness for a value encoding/json read.
func jsTruthyJSON(v any) bool {
	switch t := v.(type) {
	case float64:
		return jsTruthy(t)
	case map[string]any, []any:
		return true
	}
	return jsTruthy(v)
}
