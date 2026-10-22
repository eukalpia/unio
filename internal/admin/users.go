package admin

import (
	"net/http"
	"time"

	"github.com/eukalpia/unio/internal/auth"
	"github.com/eukalpia/unio/internal/s3err"
)

// userView is one user of GET users. Policies lists the names of the named
// policies attached to the user. The IAM store has no named policies, so the
// list is always empty; see policies.go.
type userView struct {
	Name       string    `json:"name"`
	Enabled    bool      `json:"enabled"`
	Policies   []string  `json:"policies"`
	AccessKeys []keyView `json:"accessKeys"`
}

// keyView is one access key of a user. It never carries the secret.
type keyView struct {
	ID      string `json:"id"`
	Created string `json:"created"`
}

// keyReply is the body of POST users/{name}/keys. SecretKey is shown once.
type keyReply struct {
	AccessKeyID string `json:"accessKeyId"`
	SecretKey   string `json:"secretKey"`
}

// errKeyNotFound is an access key that does not belong to the named user.
func errKeyNotFound() error {
	return &httpErr{status: http.StatusNotFound, code: s3err.NoSuchEntity, msg: "The access key does not exist."}
}

// listUsers answers GET users, sorted by name. The root account is not an IAM
// user and is not listed.
func (s *Service) listUsers(c *call) (Response, error) {
	users, err := s.cfg.IAM.ListUsers(c.ctx)
	if err != nil {
		return Response{}, err
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		keys, err := s.cfg.IAM.ListAccessKeys(c.ctx, u.Name)
		if err != nil {
			if s3err.Is(err, s3err.NoSuchEntity) {
				// Deleted between the listing and this call.
				continue
			}
			return Response{}, err
		}
		view := userView{
			Name:       u.Name,
			Enabled:    u.Enabled,
			Policies:   []string{},
			AccessKeys: make([]keyView, 0, len(keys)),
		}
		for _, k := range keys {
			view.AccessKeys = append(view.AccessKeys, keyView{ID: k.ID, Created: k.Created.UTC().Format(time.RFC3339)})
		}
		out = append(out, view)
	}
	return jsonResponse(http.StatusOK, out)
}

// createUser answers POST users.
func (s *Service) createUser(c *call) (Response, error) {
	var in nameBody
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	if err := s.checkUserName(in.Name); err != nil {
		return Response{}, err
	}
	if _, err := s.cfg.IAM.CreateUser(c.ctx, in.Name); err != nil {
		return Response{}, err
	}
	return jsonResponse(http.StatusCreated, nameReply(in))
}

// deleteUser answers DELETE users/{name}. The user's keys and policy go with it.
func (s *Service) deleteUser(c *call) (Response, error) {
	name := c.params[0]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	if err := s.cfg.IAM.DeleteUser(c.ctx, name); err != nil {
		return Response{}, err
	}
	return emptyResponse(http.StatusNoContent), nil
}

// setUserEnabled answers POST users/{name}/enable and .../disable. A disabled
// user's keys stop authenticating at once on this node, and on the others
// within the IAM cache time.
func (s *Service) setUserEnabled(c *call, enabled bool) (Response, error) {
	name := c.params[0]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	if err := s.cfg.IAM.SetUserEnabled(c.ctx, name, enabled); err != nil {
		return Response{}, err
	}
	return emptyResponse(http.StatusNoContent), nil
}

// createKey answers POST users/{name}/keys. The secret is in the response
// only. It is not logged, and the response is marked no-store.
func (s *Service) createKey(c *call) (Response, error) {
	name := c.params[0]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	key, err := s.cfg.IAM.CreateAccessKey(c.ctx, name)
	if err != nil {
		return Response{}, err
	}
	s.cfg.Logger.Info("access key created", "request_id", c.req.RequestID, "user", name, "access_key", key.ID)
	return jsonResponse(http.StatusCreated, keyReply{AccessKeyID: key.ID, SecretKey: key.Secret})
}

// deleteKey answers DELETE users/{name}/keys/{id}. The key must belong to the
// named user; a key of another user is reported as missing.
func (s *Service) deleteKey(c *call) (Response, error) {
	name, id := c.params[0], c.params[1]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	if !auth.ValidAccessKeyID(id) {
		return Response{}, errKeyNotFound()
	}
	keys, err := s.cfg.IAM.ListAccessKeys(c.ctx, name)
	if err != nil {
		return Response{}, err
	}
	owned := false
	for _, k := range keys {
		if k.ID == id {
			owned = true
			break
		}
	}
	if !owned {
		return Response{}, errKeyNotFound()
	}
	if err := s.cfg.IAM.DeleteAccessKey(c.ctx, id); err != nil {
		return Response{}, err
	}
	return emptyResponse(http.StatusNoContent), nil
}
