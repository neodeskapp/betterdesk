package filetransfer_test

import (
	"encoding/base64"
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
	{88, "FT2", 200, `{"action":"chunk_ack","transferId":"12db0ec02320087958183cbb162b10a8","chunksReceived":3,"totalChunks":1,"bytesTransferred":16,"totalBytes":12,"percent":133.33333333333331,"status":"completed"}`},
	{89, "FT2", 200, `{"transferId":"12db0ec02320087958183cbb162b10a8","status":"completed","bytesTransferred":16,"totalBytes":12,"chunksReceived":3,"totalChunks":1,"percent":133.33333333333331,"filename":"q3_report.pdf","direction":"download"}`},
	// The device sends no chunk into an upload
	{102, "FT2", 200, `{"action":"error","error":"Transfer is an upload"}`},
	{103, "FT2", 200, `{"transferId":"c00386451099cce4b54bdd85a93d0164","status":"pending","bytesTransferred":0,"totalBytes":1,"chunksReceived":0,"totalChunks":1,"percent":0,"filename":"n.txt","direction":"upload"}`},
}

// fixDeviceVectors puts the console's answers in place of Node's for the
// vectors deviceFixes names. A listed vector that already holds that
// answer stops the test: the list must name changes only.
func fixDeviceVectors(t *testing.T, vectors []deviceVector) {
	t.Helper()
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
