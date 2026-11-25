package admin

import (
	"strings"
)

// routeID names one endpoint of the admin API.
type routeID uint8

// The endpoints. The order is not significant.
const (
	routeNone routeID = iota
	routeLogin
	routeSessionGet
	routeLogout
	routeCluster
	routeMetricsSummary
	routeBucketsList
	routeBucketsCreate
	routeBucketDelete
	routeObjectsList
	routeObjectDelete
	routePresign
	routeUsersList
	routeUsersCreate
	routeUserDelete
	routeUserEnable
	routeUserDisable
	routeKeyCreate
	routeKeyDelete
	routePoliciesList
	routePolicyPut
	routePolicyDelete
	routePolicyAttach
	routePolicyDetach
)

// routeSpec maps a path pattern to the endpoint of each method. A segment
// beginning with ":" is a parameter, which matches any non-empty segment.
type routeSpec struct {
	pattern string
	methods map[string]routeID
}

// routeTable lists the endpoints. A path matches at most one pattern.
var routeTable = []routeSpec{
	{"session", map[string]routeID{"POST": routeLogin, "GET": routeSessionGet, "DELETE": routeLogout}},
	{"cluster", map[string]routeID{"GET": routeCluster}},
	{"metrics/summary", map[string]routeID{"GET": routeMetricsSummary}},
	{"buckets", map[string]routeID{"GET": routeBucketsList, "POST": routeBucketsCreate}},
	{"buckets/:bucket", map[string]routeID{"DELETE": routeBucketDelete}},
	{"buckets/:bucket/objects", map[string]routeID{"GET": routeObjectsList, "DELETE": routeObjectDelete}},
	{"buckets/:bucket/presign", map[string]routeID{"POST": routePresign}},
	{"users", map[string]routeID{"GET": routeUsersList, "POST": routeUsersCreate}},
	{"users/:user", map[string]routeID{"DELETE": routeUserDelete}},
	{"users/:user/enable", map[string]routeID{"POST": routeUserEnable}},
	{"users/:user/disable", map[string]routeID{"POST": routeUserDisable}},
	{"users/:user/keys", map[string]routeID{"POST": routeKeyCreate}},
	{"users/:user/keys/:key", map[string]routeID{"DELETE": routeKeyDelete}},
	{"users/:user/policies", map[string]routeID{"POST": routePolicyAttach}},
	{"users/:user/policies/:policy", map[string]routeID{"DELETE": routePolicyDetach}},
	{"policies", map[string]routeID{"GET": routePoliciesList}},
	{"policies/:policy", map[string]routeID{"PUT": routePolicyPut, "DELETE": routePolicyDelete}},
}

// match finds the endpoint for method and path. It returns the endpoint and the
// raw path parameters in pattern order. An unknown path is 404; a known path
// with another method is 405. Nothing is decoded, so a parameter reaches the
// handler exactly as sent.
func match(method, path string) (routeID, []string, error) {
	rest, ok := strings.CutPrefix(path, BasePath)
	if !ok || !strings.HasPrefix(rest, "/") {
		return routeNone, nil, errNotFound()
	}
	segs := strings.Split(rest[1:], "/")
	for _, spec := range routeTable {
		params, ok := matchPattern(spec.pattern, segs)
		if !ok {
			continue
		}
		id, ok := spec.methods[method]
		if !ok {
			return routeNone, nil, errMethodNotAllowed()
		}
		return id, params, nil
	}
	return routeNone, nil, errNotFound()
}

// matchPattern reports whether segs matches pattern, and returns the parameters.
func matchPattern(pattern string, segs []string) ([]string, bool) {
	pats := strings.Split(pattern, "/")
	if len(pats) != len(segs) {
		return nil, false
	}
	var params []string
	for i, p := range pats {
		switch {
		case strings.HasPrefix(p, ":"):
			if segs[i] == "" {
				return nil, false
			}
			params = append(params, segs[i])
		case p != segs[i]:
			return nil, false
		}
	}
	return params, true
}
