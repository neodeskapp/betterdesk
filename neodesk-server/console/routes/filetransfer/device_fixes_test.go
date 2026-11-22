package filetransfer_test

import (
	"encoding/base64"
	"strings"
	"testing"
)

// deviceFix is a vector of testdata/device_vectors.json whose answer a
// bug fix of BUGS.md changed on purpose: the console's status and body,
// with the transfer ids of the Node run.
type deviceFix struct {
	vector int
	bug    string
	status int
	body   string
}

var deviceFixes = []deviceFix{
	// device-b's chunk into and cancel of device-a's transfer are refused;
	// the transfer goes on with device-a's own chunks
	{85, "FT2", 200, `{"action":"error","error":"Transfer not found"}`},
	{86, "FT2", 200, `{"action":"error","error":"Transfer not found"}`},
	{87, "FT2", 200, `{"transferId":"12db0ec02320087958183cbb162b10a8","status":"completed","bytesTransferred":12,"totalBytes":12,"chunksReceived":2,"totalChunks":1,"percent":100,"filename":"q3_report.pdf","direction":"download"}`},
	// A chunk beyond the size the transfer announced is refused
	{88, "FT3", 200, `{"action":"error","error":"Chunk goes beyond the size of the transfer (12 bytes)"}`},
	{89, "FT3", 200, `{"transferId":"12db0ec02320087958183cbb162b10a8","status":"completed","bytesTransferred":12,"totalBytes":12,"chunksReceived":2,"totalChunks":1,"percent":100,"filename":"q3_report.pdf","direction":"download"}`},
	// A transfer for the device 123 as a number is refused, so there is
	// none to read, write into or list
	{100, "FT10", 400, `{"error":"device_id must be text"}`},
	{101, "FT10", 404, `{"error":"Transfer not found"}`},
	{102, "FT10", 200, `{"action":"error","error":"Transfer not found"}`},
	{103, "FT10", 404, `{"error":"Transfer not found"}`},
}

// fixDeviceVectors puts the console's answers in place of Node's for the
// vectors deviceFixes names, and takes the path of a transfer's file out
// of every record Node answered (FT6). A listed vector that already holds
// that answer stops the test: the list must name changes only.
func fixDeviceVectors(t *testing.T, vectors []deviceVector) {
	t.Helper()
	paths := 0
	for i := range vectors {
		raw, err := base64.StdEncoding.DecodeString(vectors[i].Body)
		if err != nil || !strings.HasPrefix(vectors[i].Headers["content-type"], "application/json") {
			continue
		}
		if kept, had := withoutTempPath(string(raw)); had {
			vectors[i].Body = base64.StdEncoding.EncodeToString([]byte(kept))
			paths++
		}
	}
	if paths == 0 {
		t.Fatal("no answer names the path of a file: the fix of FT6 changes no vector")
	}
	for _, fix := range deviceFixes {
		if fix.vector >= len(vectors) {
			t.Fatalf("the fixed vector %d (%s) is not in the file", fix.vector, fix.bug)
		}
		v := &vectors[fix.vector]
		body := base64.StdEncoding.EncodeToString([]byte(fix.body))
		if v.Status == fix.status && v.Body == body {
			t.Fatalf("vector %d is listed for %s but Node answered the same", fix.vector, fix.bug)
		}
		v.Status, v.Body = fix.status, body
	}
}
