package admin

import (
	"context"
	"net/url"
)

// Service serves the admin API. It keeps no per-request state, so it is safe
// for concurrent use. Create it with New.
type Service struct {
	cfg     Config
	key     []byte
	limiter *loginLimiter
	// dummy is the secret a login with an unknown key is checked against.
	dummy []byte
}

// call is one admin request after routing: the request, the caller once it is
// authenticated, the parsed query, the raw path parameters and the bounded body.
type call struct {
	ctx    context.Context
	req    Request
	p      principal
	query  url.Values
	params []string
	body   []byte
}

// Serve answers one admin request. It returns a response for every request,
// the refusals included, and does not panic on malformed input.
//
// The order is fixed: the route is matched, then the caller is authenticated
// and shown to hold unio:Admin, then the CSRF token is checked, and only then
// is the body read and the handler run. Nothing is read from or written to
// storage before the caller is authorized. The login and logout endpoints
// take no signature; the login is rate limited before anything else.
func (s *Service) Serve(ctx context.Context, req Request) Response {
	id, params, err := match(req.Method, req.Path)
	if err != nil {
		return s.reject(req, err)
	}
	query, err := url.ParseQuery(req.Query)
	if err != nil {
		return s.reject(req, errBadRequest("The query string is not valid."))
	}
	c := &call{ctx: ctx, req: req, query: query, params: params}
	if id == routeLogin || id == routeLogout {
		resp, err := s.dispatch(id, c)
		if err != nil {
			return s.reject(req, err)
		}
		return resp
	}
	p, err := s.authenticate(ctx, req, query)
	if err != nil {
		return s.reject(req, err)
	}
	if err := s.checkCSRF(req, p); err != nil {
		return s.reject(req, err)
	}
	body, err := s.requestBody(p)
	if err != nil {
		return s.reject(req, err)
	}
	c.p, c.body = p, body
	resp, err := s.dispatch(id, c)
	if err != nil {
		return s.reject(req, err)
	}
	return resp
}

// dispatch runs the handler of an endpoint and returns its success response.
func (s *Service) dispatch(id routeID, c *call) (Response, error) {
	switch id {
	case routeLogin:
		return s.login(c)
	case routeSessionGet:
		return s.sessionGet(c)
	case routeLogout:
		return s.logout(c)
	case routeCluster:
		return s.cluster(c)
	case routeMetricsSummary:
		return s.metricsSummary(c)
	case routeBucketsList:
		return s.listBuckets(c)
	case routeBucketsCreate:
		return s.createBucket(c)
	case routeBucketDelete:
		return s.deleteBucket(c)
	case routeObjectsList:
		return s.listObjects(c)
	case routeObjectDelete:
		return s.deleteObject(c)
	case routePresign:
		return s.presign(c)
	case routeUsersList:
		return s.listUsers(c)
	case routeUsersCreate:
		return s.createUser(c)
	case routeUserDelete:
		return s.deleteUser(c)
	case routeUserEnable:
		return s.setUserEnabled(c, true)
	case routeUserDisable:
		return s.setUserEnabled(c, false)
	case routeKeyCreate:
		return s.createKey(c)
	case routeKeyDelete:
		return s.deleteKey(c)
	case routePoliciesList, routePolicyPut, routePolicyDelete, routePolicyAttach, routePolicyDetach:
		return Response{}, errNamedPoliciesUnsupported()
	}
	return Response{}, errNotFound()
}

// reject turns err into the error response of req and logs it. A server
// failure is logged as an error, and a refusal at info. The body, the
// signature, the cookie and every secret are never logged.
func (s *Service) reject(req Request, err error) Response {
	status, code, _ := describe(err)
	if status >= 500 {
		s.cfg.Logger.Error("admin request failed",
			"request_id", req.RequestID, "method", req.Method, "path", req.Path, "error", err.Error())
	} else {
		s.cfg.Logger.Info("admin request refused",
			"request_id", req.RequestID, "method", req.Method, "path", req.Path,
			"status", status, "code", string(code), "remote", req.RemoteAddr)
	}
	return errorResponse(req.RequestID, err)
}

// bucketOwner names the owner of a bucket created through the API: the user
// that holds the session, and "unio" for the root account. It matches the
// owner the S3 front end gives a bucket created by the same principal.
func bucketOwner(p principal) string {
	if p.identity.User != "" {
		return p.identity.User
	}
	return "unio"
}
