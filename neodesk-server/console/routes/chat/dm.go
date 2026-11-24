package chat

import "github.com/neodeskapp/neodesk/neodesk-server/console/routes/internal/jsjson"

// sentBy is a history of a direct message conversation ("operator:<name>",
// every message to that console user) cut to what name sent: the only
// part another user may read of it (L492). A value that is no list is
// kept as it is.
func sentBy(list jsjson.Value, name string) jsjson.Value {
	if list.Kind() != jsjson.Array {
		return list
	}
	kept := [][]byte{}
	for _, item := range list.Items() {
		from := item.Get("from_id")
		sender := item.Get("from_name")
		mine := from.Kind() == jsjson.String && (from.Str() == "operator:"+name ||
			from.Str() == "operator" && sender.Kind() == jsjson.String && sender.Str() == name)
		if mine {
			b, _ := item.MarshalJSON()
			kept = append(kept, b)
		}
	}
	out, err := jsjson.Parse(string(array(kept)))
	if err != nil {
		return jsjson.Value{}
	}
	return out
}
