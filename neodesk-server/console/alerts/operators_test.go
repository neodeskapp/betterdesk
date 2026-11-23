package alerts

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"
)

// objectMembers are the names every JavaScript object has, which the Node
// console looked up as if they were operators.
var objectMembers = []string{"constructor", "toString", "isPrototypeOf", "__defineGetter__", "__defineSetter__", "hasOwnProperty",
	"__lookupGetter__", "__lookupSetter__", "propertyIsEnumerable", "valueOf", "__proto__", "toLocaleString"}

// The engine knows six operators and nothing else: every other name is
// false and fails nothing, so a rule with such a name raises no alert and
// does not stop the rules after it (BUGS AE2).
func TestOnlySixOperators(t *testing.T) {
	for _, op := range append(objectMembers, "GT", "", "gt ", "length") {
		for _, value := range []jsbind.Value{jsbind.Number(5), jsbind.String("x"), jsbind.Null} {
			if held, err := EvalCondition(value, op, jsbind.Number(1)); held || err != nil {
				t.Errorf("EvalCondition(%s, %q, 1) = %v, %v; want false", value.JSON(), op, held, err)
			}
		}
	}
	for op, want := range map[string]bool{"gt": true, "gte": true, "lt": false, "lte": false, "eq": false, "neq": true} {
		if held, err := EvalCondition(jsbind.Number(5), op, jsbind.Number(1)); held != want || err != nil {
			t.Errorf("EvalCondition(5, %q, 1) = %v, %v; want %v", op, held, err, want)
		}
	}
}

// memberOperatorRules returns the ids, as dumped, of the rules of a
// recorded case whose operator is a name every object has.
func memberOperatorRules(rules []map[string]string) []string {
	var ids []string
	for _, rule := range rules {
		var op string
		if json.Unmarshal([]byte(rule["condition_op"]), &op) == nil && slices.Contains(objectMembers, op) {
			ids = append(ids, rule["id"])
		}
	}
	return ids
}
