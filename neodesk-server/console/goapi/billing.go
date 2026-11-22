package goapi

import (
	"context"
	"net/http"
)

// BillingSession is the answer of GET /api/billing/sessions/{id}.
type BillingSession struct {
	Session *Object
	// Device is the session's device, nil when it has no row or lies
	// outside the device scope of the identity the console calls with.
	Device *Object
}

// DeviceID is the id of the session's device, "" when it names none.
func (b *BillingSession) DeviceID() string {
	id, _ := b.Session.Get("device_id").(string)
	return id
}

// GetBillingSession calls GET /api/billing/sessions/{id}. It returns nil
// and no error when the API server has no session of that id (404); any
// other failure is the error.
func (c *Client) GetBillingSession(ctx context.Context, id string) (*BillingSession, error) {
	resp, e := c.do(ctx, http.MethodGet, "/billing/sessions/"+seg(id))
	if e != nil {
		if e.Status() == http.StatusNotFound {
			return nil, nil
		}
		return nil, e
	}
	data, _ := resp.Data().(*Object)
	if data == nil {
		return nil, &Error{Message: "billing session answer is not an object"}
	}
	session, _ := data.Get("session").(*Object)
	if session == nil {
		return nil, &Error{Message: "billing session answer has no session"}
	}
	device, _ := data.Get("device").(*Object)
	return &BillingSession{Session: session, Device: device}, nil
}
