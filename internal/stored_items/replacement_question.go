package stored_items

import (
	"bytes"
	"context"
	"fmt"
	"net/http"

	"github.com/qlustered/qctl/internal/api"
)

// ReplacementQuestion asks whether a file replaces an older file of its cloud
// source that did not load, with its answer once given.
type ReplacementQuestion = api.StoredItemReplacementQuestionSchema

// ReplacementAnswer is discard_old or keep_both.
type ReplacementAnswer = api.ReplacementAnswer

// GetReplacementQuestion reads the replacement question of a file with
// GET /stored-items/{id}/replacement-question. It returns nil when the server
// answers null. A non-2xx answer is an *APIError carrying the response's
// error_code.
func (c *Client) GetReplacementQuestion(accessToken string, storedItemID int) (*ReplacementQuestion, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.GetStoredItemReplacementQuestionWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.GetStoredItemReplacementQuestionParams{})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if bytes.Equal(bytes.TrimSpace(resp.Body), []byte("null")) {
		return nil, nil
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}

// AnswerReplacementQuestion records answer to the replacement question of a
// file with POST /stored-items/{id}/replacement-question and returns the
// answered question. A non-2xx answer is an *APIError carrying the response's
// error_code.
func (c *Client) AnswerReplacementQuestion(accessToken string, storedItemID int, answer ReplacementAnswer) (*ReplacementQuestion, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return nil, err
	}
	resp, err := apiClient.API.AnswerStoredItemReplacementQuestionWithResponse(
		context.Background(), c.organizationID, storedItemID,
		&api.AnswerStoredItemReplacementQuestionParams{},
		api.StoredItemReplacementAnswerSchema{Answer: answer})
	if err != nil {
		return nil, wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return nil, handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return nil, fmt.Errorf("unexpected empty response")
	}
	return resp.JSON200, nil
}
