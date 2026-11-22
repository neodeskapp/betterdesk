package signal

import (
	"fmt"
	"testing"
	"time"

	"github.com/unitronix/betterdesk-server/config"
	pb "github.com/unitronix/betterdesk-server/proto"
	"github.com/unitronix/betterdesk-server/ratelimit"
)

func TestRegisterPkForNewIDsIsRateLimited(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeOpen)
	limiter := ratelimit.NewIPLimiter(2, time.Minute, time.Minute)
	t.Cleanup(limiter.Stop)
	srv.SetRateLimiter(limiter)

	for i := 0; i < 5; i++ {
		srv.processRegisterPk(newRegisterPk(fmt.Sprintf("NEWID%04d", i)), "198.51.100.30:5000")
	}

	stored := 0
	for i := 0; i < 5; i++ {
		if p, _ := database.GetPeer(fmt.Sprintf("NEWID%04d", i)); p != nil {
			stored++
		}
	}
	if stored != 2 {
		t.Fatalf("%d of 5 new ids from one address were stored, want 2", stored)
	}

	// A known id is not counted: its RegisterPk still goes through.
	if got := registerPkResult(srv.processRegisterPk(newRegisterPk("NEWID0000"), "198.51.100.30:5001")); got != pb.RegisterPkResponse_OK {
		t.Fatalf("RegisterPk of a known id = %v, want OK", got)
	}
}

func TestPendingEnrollmentQueueIsCapped(t *testing.T) {
	srv, database := newTestSignalServer(t, config.EnrollmentModeManaged)
	srv.maxPendingEnrollments = 3

	for i := 0; i < 6; i++ {
		srv.processRegisterPk(newRegisterPk(fmt.Sprintf("PEND%04d", i)), fmt.Sprintf("198.51.100.%d:5000", 40+i))
	}

	entries, err := database.ListConfigByPrefix("pending_device_")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("approval queue holds %d entries, want the cap of 3", len(entries))
	}
}
