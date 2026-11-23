package dashboard

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/console/kit"
	"github.com/neodeskapp/neodesk/neodesk-server/console/serverinfo"
)

// probeTimeout is how long a port probe waits for the connection.
const probeTimeout = 2 * time.Second

// service is the state of one part of the server: "running", "stopped"
// or "unknown".
type service struct {
	Status string `json:"status"`
}

type statusData struct {
	Backend string `json:"backend"`
	// Uptime is the console's own, in whole seconds.
	Uptime int64   `json:"uptime"`
	Hbbs   service `json:"hbbs"`
	Hbbr   service `json:"hbbr"`
	Signal service `json:"signal"`

	APIPort       int `json:"api_port"`
	SignalPort    int `json:"signal_port"`
	RelayPort     int `json:"relay_port"`
	NATPort       int `json:"nat_port"`
	WSSignalPort  int `json:"ws_signal_port"`
	WSRelayPort   int `json:"ws_relay_port"`
	ClientAPIPort int `json:"client_api_port"`
	ConsolePort   int `json:"console_port"`
}

// serverStatus answers GET /api/server/status: whether the API, the
// signal server and the relay run, and the ports of the install.
//
// The three are one program: when the API answers its health check, the
// other two run as well. Only when it does not are the signal and relay
// ports probed, the relay first, each for up to two seconds.
func (h *handlers) serverStatus(w http.ResponseWriter, r *http.Request) {
	cfg := h.d.Config
	health := h.backend.GetHealth(r.Context())
	api := service{"stopped"}
	relay, signal := service{"running"}, service{"running"}
	if health.Get("status") == "running" {
		api.Status = "running"
	} else {
		relay = h.probe(r, cfg.RelayHost, cfg.RelayPort)
		signal = h.probe(r, cfg.SignalHost, cfg.SignalPort)
	}

	// An API address that cannot be read fails the request, after the
	// probes
	text, ok := serverinfo.URLPort(cfg.APIURL)
	if !ok {
		h.log.Error("Server status error: the API address is not a URL")
		serverError(w, r)
		return
	}
	apiPort, _ := strconv.Atoi(text)
	if apiPort == 0 {
		apiPort = cfg.GoAPIPort
	}
	if apiPort == 0 {
		apiPort = 21114
	}
	clientAPIPort := apiPort
	if cfg.APIEnabled {
		clientAPIPort = cfg.APIPort
	}

	kit.OK(w, r, statusData{
		Backend:       "neodesk",
		Uptime:        int64(h.now().Sub(h.started) / time.Second),
		Hbbs:          api,
		Hbbr:          relay,
		Signal:        signal,
		APIPort:       apiPort,
		SignalPort:    cfg.SignalPort,
		RelayPort:     cfg.RelayPort,
		NATPort:       cfg.SignalPort - 1,
		WSSignalPort:  cfg.SignalPort + 2,
		WSRelayPort:   cfg.RelayPort + 2,
		ClientAPIPort: clientAPIPort,
		ConsolePort:   cfg.Port,
	})
}

// probe reports whether something accepts TCP connections on a port:
// "running" when it does, "stopped" when the connection is refused or
// takes longer than two seconds, "unknown" for a number that is no port.
func (h *handlers) probe(r *http.Request, host string, port int) service {
	if port < 0 || port > 65535 {
		return service{"unknown"}
	}
	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()
	conn, err := h.dial(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return service{"stopped"}
	}
	_ = conn.Close()
	return service{"running"}
}
