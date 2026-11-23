//go:build contract

package contract_test

import (
	"net/url"

	"github.com/neodeskapp/neodesk/neodesk-server/console/contract"
)

// The server's keys and addresses: the public key as JSON and as a file,
// the API key masked and in full, the addresses a client is given, and the
// QR code of the client configuration, each with the hosts a caller can
// ask for.
func init() {
	some := contract.Roles("signed-out", "viewer", "admin")
	host := func(value string) url.Values { return url.Values{"host": {value}} }

	cases := []contract.Case{
		contract.Case{Name: "public", Path: "/api/keys/public"},
		contract.Case{Name: "public-head", Method: "HEAD", Path: "/api/keys/public", Roles: some},
		contract.Case{Name: "public-other-spelling", Path: "/API/Keys/Public/", Roles: some},
		contract.Case{
			Name: "public-not-modified", Path: "/api/keys/public",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},

		contract.Case{Name: "config-qr", Path: "/api/keys/public/qr"},
		contract.Case{Name: "config-qr-for-a-host", Path: "/api/keys/public/qr", Query: host("console.example.org")},
		contract.Case{Name: "config-qr-for-a-host-with-scheme-and-port", Path: "/api/keys/public/qr", Query: host("https://Console.Example.ORG:8443/path"), Roles: some},
		contract.Case{Name: "config-qr-for-localhost", Path: "/api/keys/public/qr", Query: host("localhost"), Roles: some},
		contract.Case{Name: "config-qr-for-a-local-name", Path: "/api/keys/public/qr", Query: host("printer.local"), Roles: some},
		contract.Case{Name: "config-qr-for-an-ipv6-address", Path: "/api/keys/public/qr", Query: host("2001:db8::1"), Roles: some},
		contract.Case{Name: "config-qr-for-an-invalid-host", Path: "/api/keys/public/qr", Query: host("not a host!"), Roles: some},
		contract.Case{Name: "config-qr-for-an-empty-host", Path: "/api/keys/public/qr", Query: host(""), Roles: some},
		contract.Case{Name: "config-qr-for-two-hosts", Path: "/api/keys/public/qr", Query: url.Values{"host": {"a.example.org", "b.example.org"}}, Roles: some},
		contract.Case{Name: "config-qr-for-a-host-object", Path: "/api/keys/public/qr", Query: url.Values{"host[x]": {"a.example.org"}}, Roles: some},
		contract.Case{
			Name: "config-qr-behind-a-proxy", Path: "/api/keys/public/qr",
			Headers: map[string]string{"X-Forwarded-Host": "proxy.example.org"}, Roles: some,
		},
		contract.Case{Name: "config-qr-head", Method: "HEAD", Path: "/api/keys/public/qr", Roles: some},

		contract.Case{Name: "download", Path: "/api/keys/public/download"},
		contract.Case{Name: "download-head", Method: "HEAD", Path: "/api/keys/public/download", Roles: some},
		contract.Case{
			Name: "download-not-modified", Path: "/api/keys/public/download",
			Headers: map[string]string{"If-None-Match": "*"}, Roles: some,
		},
		contract.Case{
			Name: "download-as-a-page", Path: "/api/keys/public/download",
			Headers: map[string]string{"Accept": "text/html"}, Roles: some,
		},

		contract.Case{Name: "api-key-masked", Path: "/api/keys/api"},
		contract.Case{Name: "api-key-in-full", Path: "/api/keys/api", Query: url.Values{"show": {"true"}}},
		contract.Case{Name: "api-key-show-false", Path: "/api/keys/api", Query: url.Values{"show": {"false"}}, Roles: some},
		contract.Case{Name: "api-key-show-in-capitals", Path: "/api/keys/api", Query: url.Values{"show": {"TRUE"}}},
		contract.Case{Name: "api-key-show-one", Path: "/api/keys/api", Query: url.Values{"show": {"1"}}, Roles: some},
		contract.Case{Name: "api-key-show-twice", Path: "/api/keys/api", Query: url.Values{"show": {"true", "true"}}},
		contract.Case{Name: "api-key-show-object", Path: "/api/keys/api", Query: url.Values{"show[x]": {"true"}}, Roles: some},
		contract.Case{Name: "api-key-head", Method: "HEAD", Path: "/api/keys/api", Query: url.Values{"show": {"true"}}, Roles: contract.Roles("signed-out", "viewer", "operator", "admin")},

		contract.Case{Name: "server-info", Path: "/api/keys/server-info"},
		contract.Case{Name: "server-info-for-a-host", Path: "/api/keys/server-info", Query: host("console.example.org")},
		contract.Case{Name: "server-info-for-an-address", Path: "/api/keys/server-info", Query: host("192.0.2.7:21116"), Roles: some},
		contract.Case{Name: "server-info-for-an-invalid-host", Path: "/api/keys/server-info", Query: host("a_b/c"), Roles: some},
		contract.Case{Name: "server-info-for-two-hosts", Path: "/api/keys/server-info", Query: url.Values{"host": {"a.example.org", "b.example.org"}}, Roles: some},

		contract.Case{Name: "qr-alias", Path: "/api/keys/qr"},
		contract.Case{Name: "qr-alias-for-a-host", Path: "/api/keys/qr", Query: host("console.example.org")},
		contract.Case{Name: "qr-alias-for-a-local-name", Path: "/api/keys/qr", Query: host("Printer.LOCAL"), Roles: some},
	}
	// The picture of a QR code is some bytes longer or shorter from one
	// key to the next, so its text is free; the matrix golden holds the
	// form of the data URL.
	for i := range cases {
		if cases[i].Method == "" && (cases[i].Path == "/api/keys/public/qr" || cases[i].Path == "/api/keys/qr") {
			cases[i].Volatile = []string{"~data.qr"}
		}
	}
	contract.Group("keys", cases...)
}
