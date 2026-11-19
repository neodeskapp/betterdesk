package automation_test

import (
	"github.com/unitronix/betterdesk-server/console/routes/internal/replay"
)

// deviceFixes are the steps of testdata/node_device_*.json that the bug
// fixes change.
var deviceFixes replay.Fixes

// AU14: another device's command is not found.
var _ = deviceFixes.Add("AU14", map[string]replay.Fix{
	` POST /api/bd/commands/5/result {"status":"completed"}`: {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
	` POST /api/bd/commands/5/result {"status":"failed"}`:    {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
	` POST /api/bd/commands/5/result {"status":"running"}`:   {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
	` POST /api/bd/commands/6/result {"status":"running"}`:   {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
	` POST /api/bd/commands/7/result {"status":"running"}`:   {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
	` POST /api/bd/commands/8/result {"status":"running"}`:   {NodeStatus: 403, Status: 404, Answer: `{"error":"Command not found"}`},
})
