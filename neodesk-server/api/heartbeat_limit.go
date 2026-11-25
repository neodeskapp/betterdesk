package api

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/ratelimit"
)

// The client API's heartbeat and sysinfo calls are limited per device and,
// much higher, per source address (L498). An office sends from one address
// for all its computers, so a limit per address alone either drops most of
// an office's heartbeats or lets one device flood.
//
// heartbeatsPerDevice: a NeoDesk or RustDesk client sends a heartbeat every
// 15 s when idle (4 a minute) and every 3 s while a session runs (20 a
// minute), and right away when it has command results. 40 leaves twice the
// busiest steady rate.
//
// heartbeatsPerAddress: 500 computers behind one address send about 2,000
// heartbeats a minute when idle, plus sysinfo calls, plus 20 a minute for
// each one in a session, and all of them at once after a power cut. 6,000
// a minute is three times the steady load of such an office and still
// stops one address at 100 calls a second.
const (
	heartbeatsPerDevice  = 40
	heartbeatsPerAddress = 6000
)

// heartbeatDropLogGap is how often a drop is logged per address and limit.
const heartbeatDropLogGap = time.Minute

// allowClientAPIAddress counts a client API call against the ceiling of its
// address. When the address is over it, the call is answered with 429 and
// Retry-After, logged and counted, and false is returned.
func (s *Server) allowClientAPIAddress(w http.ResponseWriter, ip, route string) bool {
	ok, wait := s.heartbeatLimiter.Allow(ratelimit.AddressKey(ip), time.Now())
	if ok {
		return true
	}
	s.dropClientAPICall(w, "address", ip, route, wait)
	return false
}

// allowClientAPIDevice counts a heartbeat against the ceiling of its device.
// key names the device: its id once the device proved it with its signed
// envelope, else the id together with the address, so that nobody can use
// up another device's allowance by sending its id.
func (s *Server) allowClientAPIDevice(w http.ResponseWriter, key, ip, route string) bool {
	ok, wait := s.heartbeatDeviceLimiter.Allow(key, time.Now())
	if ok {
		return true
	}
	s.dropClientAPICall(w, "device", ip, route, wait)
	return false
}

// dropClientAPICall answers a refused call with 429 and the seconds until
// its window ends. NeoDesk and RustDesk clients read the body of a 4xx
// answer like any other: the heartbeat finds no instructions in it and
// sends the next one on its schedule, sysinfo tries again two minutes
// later. One log line per address and limit a minute; every drop counts in
// neodesk_client_api_dropped_total.
func (s *Server) dropClientAPICall(w http.ResponseWriter, limit, ip, route string, wait time.Duration) {
	if s.metrics != nil {
		if limit == "device" {
			s.metrics.ClientAPIDropsDevice.Add(1)
		} else {
			s.metrics.ClientAPIDropsAddress.Add(1)
		}
	}
	now := time.Now()
	key := limit + "|" + ratelimit.AddressKey(ip)
	s.heartbeatDropMu.Lock()
	logIt := now.Sub(s.heartbeatDropLog[key]) >= heartbeatDropLogGap
	if logIt {
		if len(s.heartbeatDropLog) >= 10_000 {
			for k, at := range s.heartbeatDropLog {
				if now.Sub(at) >= heartbeatDropLogGap {
					delete(s.heartbeatDropLog, k)
				}
			}
		}
		s.heartbeatDropLog[key] = now
	}
	s.heartbeatDropMu.Unlock()
	if logIt {
		ceiling := s.heartbeatLimiter.Limit()
		if limit == "device" {
			ceiling = s.heartbeatDeviceLimiter.Limit()
		}
		log.Printf("[api] client API %s from %s refused: over the %s limit of %d a minute (further drops from it are not logged for a minute)",
			route, ip, limit, ceiling)
	}
	seconds := int(wait.Round(time.Second) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(seconds))
	writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate_limited", "retry_after": seconds})
}
