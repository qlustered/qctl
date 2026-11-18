package stored_items

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/qlustered/qctl/internal/api"
)

// CodeBrokerReportRefused is the 409 for a file not ready for an issues
// report; context.reason says why.
const CodeBrokerReportRefused = "BROKER_REPORT_REFUSED"

// verbSlot stands in refusalAdvice for what the caller does with the report.
const verbSlot = "{verb}"

// refusalAdvice is the advice for each BROKER_REPORT_REFUSED context.reason,
// which a file_download_failed row also carries as broker_report_refusal.
var refusalAdvice = map[string]string{
	"waiting":             verbSlot + " again once the file's latest ingestion job has finished.",
	"stopped_on_our_side": "resolve the alert holding the file's ingestion job, then " + verbSlot + " again once the job has finished.",
	"not_loaded":          "the file did not load, so it has no issues report; the alert on it says what the broker needs to fix.",
	"nothing_new":         "the file is unchanged from an earlier delivery, so it has no issues report to " + verbSlot + ".",
	"partitioned":         "a file split into parts has no issues report to " + verbSlot + ".",
	"not_used":            "the MGA did not use this file, so it has no issues report to " + verbSlot + ".",
}

// IssuesReportRefusalAdvice returns the advice for an issues report of file
// fileID refused for reason, verb naming what the caller does with the report
// ("send", "download"). For "waiting" and for a reason it does not know it
// also names 'qctl describe file <fileID>'.
func IssuesReportRefusalAdvice(reason string, fileID int, verb string) string {
	advice := strings.ReplaceAll(refusalAdvice[reason], verbSlot, verb)
	if advice == "" || reason == "waiting" {
		advice = strings.TrimSpace(advice + fmt.Sprintf(" 'qctl describe file %d' shows the file's state.", fileID))
	}
	return advice
}

// PutIssuesReportForDownload queues a file's issues report workbook for
// download with POST /stored-items/{id}/issues-report and returns the queued
// job's download_handle, or "" for a 200 with no JSON body. A non-2xx
// answer is an *APIError carrying the response's error_code and context.
func (c *Client) PutIssuesReportForDownload(accessToken string, storedItemID int) (string, error) {
	apiClient, err := c.newAPIClient(accessToken)
	if err != nil {
		return "", err
	}
	resp, err := apiClient.API.PutBrokerIssuesReportForDownloadWithResponse(
		context.Background(), c.organizationID, storedItemID, &api.PutBrokerIssuesReportForDownloadParams{})
	if err != nil {
		return "", wrapAPIError(apiClient, err, "request failed")
	}
	if resp.StatusCode() != http.StatusOK {
		return "", handleError(resp.StatusCode(), resp.Body, "request failed")
	}
	if resp.JSON200 == nil {
		return "", nil
	}
	return resp.JSON200.DownloadHandle, nil
}
