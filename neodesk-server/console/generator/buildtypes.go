package generator

import "github.com/neodeskapp/neodesk/neodesk-server/console/jsbind"

// ProductSupport is the one product the generator builds: the NeoDesk
// Support client. Rows of the old Go support agent ("agent",
// "support-agent", ...) count as this product.
const ProductSupport = "neodesk-support"

// canonicalProductType maps the product names rows have used to
// ProductSupport, and anything else to "".
func canonicalProductType(raw any) string {
	value := ""
	if raw != nil {
		value = jsbind.ToLower(jsTrim(jsString(raw)))
	}
	switch value {
	case ProductSupport, "neodesk_support", "support-agent", "support_agent", "agent", "agent-client", "agent_client", "rdclient":
		return ProductSupport
	}
	return ""
}

// NormalizeProductType is the product of a row: always ProductSupport,
// whatever the column holds (lib/generatorBuildTypes.normalizeProductType).
func NormalizeProductType(raw any) string {
	if p := canonicalProductType(raw); p != "" {
		return p
	}
	return ProductSupport
}

// IsSupportBundle reports whether a bundle row's profile was made by the
// template generator (version 2): its sku or generator_kind is
// neodesk-support or its generator_version is 2 or more. Rows left from
// before have none of these. raw is the branding column: text or a parsed
// object.
func IsSupportBundle(raw any) bool {
	branding := parseBundleBranding(raw)
	if prop(branding, "sku") == ProductSupport || prop(branding, "generator_kind") == ProductSupport {
		return true
	}
	return jsNumber(prop(branding, "generator_version")) >= 2
}

// parseBundleBranding is the branding column as an object: {} for an
// empty column, text that does not parse, or JSON that is no object.
func parseBundleBranding(raw any) any {
	if !truthy(raw) {
		return nil
	}
	text, isText := raw.(string)
	if !isText {
		return raw
	}
	v, err := parseJSON(text)
	if err != nil || !isObject(v) {
		return nil
	}
	return v
}

// isQueuedBuildStatus reports "queued" and the older "pending", in any
// letter case and with space around.
func isQueuedBuildStatus(status string) bool {
	switch jsbind.ToLower(jsTrim(status)) {
	case "queued", "pending":
		return true
	}
	return false
}

// normalizeBuildStatus is the status a build row is stored with: "queued"
// for both queue names, the status in lower case otherwise, and "queued"
// for an empty one.
func normalizeBuildStatus(status string) string {
	value := jsbind.ToLower(jsTrim(status))
	if isQueuedBuildStatus(value) {
		return "queued"
	}
	if value == "" {
		return "queued"
	}
	return value
}
