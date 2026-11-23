package automation

import (
	"math"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
	"github.com/neodeskapp/neodesk/neodesk-server/console/mail"
	"github.com/neodeskapp/neodesk/neodesk-server/console/middleware"
)

// maxCooldown is the longest cooldown a rule takes, in seconds: the
// largest number an INTEGER column holds on PostgreSQL.
const maxCooldown = 2147483647

// typedFields are the fields of a rule that hold text or a number, in the
// order they are checked.
var typedFields = []string{"description", "condition_value", "scope_device_id", "cooldown_secs", "notify_emails"}

// wrongType returns the answer for the first field of a rule's body that
// has the wrong type, "" when each has its type: text for description and
// scope_device_id, a finite number for condition_value, a whole number of
// seconds from 0 for cooldown_secs, and plain addresses separated by
// commas for notify_emails (mail.PlainAddressList). null stands for a
// field that is not given. The alert engine and the alert mail work with
// what is stored, and the databases would store another type each in
// their own way or fail.
func wrongType(body middleware.JSONObject) string {
	for _, field := range typedFields {
		if !body.Has(field) {
			continue
		}
		v := jsbind.Of(body.Get(field))
		ok := v.IsNull()
		switch field {
		case "condition_value":
			ok = ok || finite(v)
		case "cooldown_secs":
			ok = ok || finite(v) && v.ToNumber() == math.Trunc(v.ToNumber()) && v.ToNumber() >= 0 && v.ToNumber() <= maxCooldown
		case "notify_emails":
			ok = ok || v.IsString() && mail.PlainAddressList(v.Str())
		default:
			ok = ok || v.IsString()
		}
		if !ok {
			return "Invalid " + field
		}
	}
	return ""
}

// finite reports whether v is a number other than an infinity.
func finite(v jsbind.Value) bool {
	return v.IsNumber() && !math.IsInf(v.ToNumber(), 0) && !math.IsNaN(v.ToNumber())
}
