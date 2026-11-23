package crypto

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
	"time"

	"github.com/neodeskapp/neodesk/neodesk-server/codec"
	pb "github.com/neodeskapp/neodesk/neodesk-server/proto"
	"golang.org/x/crypto/nacl/box"
	"golang.org/x/crypto/nacl/secretbox"
	"google.golang.org/protobuf/proto"
)

// secureHandshakeAsClient runs the client side of RustDesk's secure_tcp:
// verify the server's signed key, seal a symmetric key to it, then send one
// encrypted message. It returns the server key the client saw.
func secureHandshakeAsClient(t *testing.T, conn net.Conn, serverPub ed25519.PublicKey, msg *pb.RendezvousMessage) []byte {
	t.Helper()
	first, err := codec.ReadRawProto(conn, 5*time.Second)
	if err != nil {
		t.Errorf("client read KeyExchange: %v", err)
		return nil
	}
	keys := first.GetKeyExchange().GetKeys()
	if len(keys) != 1 || len(keys[0]) != ed25519.SignatureSize+32 {
		t.Errorf("server KeyExchange keys = %d", len(keys))
		return nil
	}
	signed := keys[0]
	if !ed25519.Verify(serverPub, signed[ed25519.SignatureSize:], signed[:ed25519.SignatureSize]) {
		t.Error("server key signature does not verify")
		return nil
	}
	var theirPub [32]byte
	copy(theirPub[:], signed[ed25519.SignatureSize:])

	ourPub, ourPriv, err := box.GenerateKey(rand.Reader)
	if err != nil {
		t.Error(err)
		return nil
	}
	var symKey [32]byte
	rand.Read(symKey[:])
	var zeroNonce [24]byte
	sealed := box.Seal(nil, symKey[:], &zeroNonce, &theirPub, ourPriv)
	if err := codec.WriteRawProto(conn, &pb.RendezvousMessage{Union: &pb.RendezvousMessage_KeyExchange{
		KeyExchange: &pb.KeyExchange{Keys: [][]byte{ourPub[:], sealed}},
	}}); err != nil {
		t.Errorf("client send KeyExchange: %v", err)
		return nil
	}

	plain, err := proto.Marshal(msg)
	if err != nil {
		t.Error(err)
		return nil
	}
	nonce := makeNonce(1)
	if err := codec.WriteRawBytes(conn, secretbox.Seal(nil, plain, &nonce, &symKey)); err != nil {
		t.Errorf("client send encrypted: %v", err)
	}
	return theirPub[:]
}

func TestSecureTCPUsesAFreshServerKeyPerConnection(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	var seen [][]byte
	for i := 0; i < 2; i++ {
		server, client := net.Pipe()
		got := make(chan []byte, 1)
		go func() {
			got <- secureHandshakeAsClient(t, client, pub, &pb.RendezvousMessage{
				Union: &pb.RendezvousMessage_Hc{Hc: &pb.HealthCheck{Token: "hello"}},
			})
		}()

		result, err := NegotiateSecureTCP(server, priv)
		if err != nil {
			t.Fatalf("handshake %d: %v", i+1, err)
		}
		if !result.Secure {
			t.Fatalf("handshake %d was not secure", i+1)
		}
		msg, err := result.SecureConn.ReadMessage(5 * time.Second)
		if err != nil {
			t.Fatalf("handshake %d: read encrypted message: %v", i+1, err)
		}
		if msg.GetHc().GetToken() != "hello" {
			t.Fatalf("handshake %d: decrypted %+v", i+1, msg)
		}
		seen = append(seen, <-got)
		server.Close()
		client.Close()
	}
	if bytes.Equal(seen[0], seen[1]) {
		t.Fatal("two connections got the same server key; a leaked signing key would open every recorded session")
	}
}
