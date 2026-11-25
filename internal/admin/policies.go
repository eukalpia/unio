package admin

import (
	"encoding/json"
	"net/http"
)

// policyView is one managed policy as the console reads it: the name and the
// document as a JSON object.
type policyView struct {
	Name     string          `json:"name"`
	Document json.RawMessage `json:"document"`
}

// policyBody is the body of PUT policies/{name}.
type policyBody struct {
	Document json.RawMessage `json:"document"`
}

// attachBody is the body of POST users/{name}/policies.
type attachBody struct {
	Policy string `json:"policy"`
}

// listPolicies answers GET policies.
func (s *Service) listPolicies(c *call) (Response, error) {
	pols, err := s.cfg.IAM.ListPolicies(c.ctx)
	if err != nil {
		return Response{}, err
	}
	out := make([]policyView, 0, len(pols))
	for _, p := range pols {
		out = append(out, policyView{Name: p.Name, Document: json.RawMessage(p.Document)})
	}
	return jsonResponse(http.StatusOK, out)
}

// putPolicy answers PUT policies/{name}. The IAM store validates the name and
// the document (MalformedPolicy for a bad one) and stores nothing on error.
func (s *Service) putPolicy(c *call) (Response, error) {
	var in policyBody
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	if len(in.Document) == 0 || in.Document[0] != '{' {
		return Response{}, errBadRequest("The policy document must be a JSON object.")
	}
	if err := s.cfg.IAM.PutPolicy(c.ctx, c.params[0], in.Document); err != nil {
		return Response{}, err
	}
	s.cfg.Logger.Info("policy stored", "request_id", c.req.RequestID, "policy", logSafe(c.params[0]))
	return emptyResponse(http.StatusNoContent), nil
}

// deletePolicy answers DELETE policies/{name}.
func (s *Service) deletePolicy(c *call) (Response, error) {
	if err := s.cfg.IAM.DeletePolicy(c.ctx, c.params[0]); err != nil {
		return Response{}, err
	}
	s.cfg.Logger.Info("policy deleted", "request_id", c.req.RequestID, "policy", logSafe(c.params[0]))
	return emptyResponse(http.StatusNoContent), nil
}

// attachPolicy answers POST users/{name}/policies. Root has every right and no
// policies, so it is refused like every other change to root.
func (s *Service) attachPolicy(c *call) (Response, error) {
	name := c.params[0]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	var in attachBody
	if err := decodeBody(c, &in); err != nil {
		return Response{}, err
	}
	if in.Policy == "" {
		return Response{}, errBadRequest("The policy name is required.")
	}
	if err := s.cfg.IAM.AttachUserPolicy(c.ctx, name, in.Policy); err != nil {
		return Response{}, err
	}
	s.cfg.Logger.Info("policy attached", "request_id", c.req.RequestID, "user", logSafe(name), "policy", logSafe(in.Policy))
	return emptyResponse(http.StatusNoContent), nil
}

// detachPolicy answers DELETE users/{name}/policies/{policy}.
func (s *Service) detachPolicy(c *call) (Response, error) {
	name, pol := c.params[0], c.params[1]
	if err := s.checkUserName(name); err != nil {
		return Response{}, err
	}
	if err := s.cfg.IAM.DetachUserPolicy(c.ctx, name, pol); err != nil {
		return Response{}, err
	}
	s.cfg.Logger.Info("policy detached", "request_id", c.req.RequestID, "user", logSafe(name), "policy", logSafe(pol))
	return emptyResponse(http.StatusNoContent), nil
}
