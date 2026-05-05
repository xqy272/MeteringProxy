package extractor

import (
	"encoding/json"
	"sync"
)

type ResponsesStreamState struct {
	mu sync.Mutex

	seenCreated  bool
	createdModel string

	seenCompleted  bool
	completedModel string
	completedUsage json.RawMessage

	seenFailed  bool
	failedModel string

	seenIncomplete   bool
	incompleteModel  string
	incompleteReason string

	seenErrorEvent bool
	errorType      string
	errorCode      string
	errorParam     string
	usageSeen      bool
	usage          *UsageInfo

	terminalEvent  string
	terminalReason string

	jsonParseErrors int64
}

func NewResponsesStreamState() *ResponsesStreamState {
	return &ResponsesStreamState{}
}

type ResponsesSSEEvent struct {
	Type     string `json:"type"`
	Response *struct {
		Model             string          `json:"model"`
		Status            string          `json:"status"`
		Usage             json.RawMessage `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`
	Error *struct {
		Type  string `json:"type"`
		Code  string `json:"code"`
		Param string `json:"param"`
	} `json:"error"`
}

func (s *ResponsesStreamState) ProcessSSEEvent(data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	text := stripSSEPrefix(string(data))
	if text == "" {
		return
	}

	var evt ResponsesSSEEvent
	if err := json.Unmarshal([]byte(text), &evt); err != nil {
		s.jsonParseErrors++
		return
	}

	switch evt.Type {
	case "response.created":
		s.seenCreated = true
		if evt.Response != nil && evt.Response.Model != "" {
			s.createdModel = evt.Response.Model
		}

	case "response.completed":
		s.seenCompleted = true
		if evt.Response != nil {
			if evt.Response.Model != "" {
				s.completedModel = evt.Response.Model
			}
			if len(evt.Response.Usage) > 0 {
				s.completedUsage = evt.Response.Usage
			}
		}

	case "response.failed":
		s.seenFailed = true
		if evt.Response != nil && evt.Response.Model != "" {
			s.failedModel = evt.Response.Model
		}

	case "response.incomplete":
		s.seenIncomplete = true
		if evt.Response != nil {
			if evt.Response.Model != "" {
				s.incompleteModel = evt.Response.Model
			}
			if evt.Response.IncompleteDetails != nil && evt.Response.IncompleteDetails.Reason != "" {
				s.incompleteReason = evt.Response.IncompleteDetails.Reason
			}
		}

	case "error":
		s.seenErrorEvent = true
		if evt.Error != nil {
			s.errorType = evt.Error.Type
			s.errorCode = evt.Error.Code
			s.errorParam = evt.Error.Param
		}
	}
}

type StreamFinalResult struct {
	Usage               *UsageInfo
	ModelReturned       string
	ModelReturnedSource string
	TerminalEvent       string
	TerminalReason      string
	CaptureOutcome      string
	CaptureReason       string
	ErrorInfo           *ErrorInfo
}

func (s *ResponsesStreamState) Result() StreamFinalResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.completedUsage != nil && len(s.completedUsage) > 0 {
		info, err := parseResponsesUsageFromCompleted(s.completedModel, s.completedUsage)
		if err == nil && info != nil && (info.InputTokens > 0 || info.OutputTokens > 0 || info.TotalTokens > 0) {
			s.usageSeen = true
			s.usage = info
		}
	}

	if s.usageSeen && s.usage != nil {
		modelReturned := s.usage.Model
		if modelReturned == "" {
			modelReturned = bestModelStr(s.completedModel, s.createdModel, s.failedModel, s.incompleteModel, "")
		}
		return StreamFinalResult{
			Usage:               s.usage,
			ModelReturned:       modelReturned,
			ModelReturnedSource: "usage",
			TerminalEvent:       "response.completed",
			CaptureOutcome:      "captured",
		}
	}

	if s.jsonParseErrors > 0 {
		return StreamFinalResult{
			ModelReturned:       bestModelStr(s.completedModel, s.incompleteModel, s.failedModel, s.createdModel, ""),
			ModelReturnedSource: "parse_error",
			TerminalEvent:       "parse_error",
			TerminalReason:      "stream_error",
			CaptureOutcome:      "failed",
			CaptureReason:       "parse_error",
			ErrorInfo:           &ErrorInfo{Class: "stream_parse_error"},
		}
	}

	if s.seenCompleted {
		modelReturned := bestModelStr(s.completedModel, s.createdModel, "", "", "")
		return StreamFinalResult{
			ModelReturned:       modelReturned,
			ModelReturnedSource: "response_completed",
			TerminalEvent:       "response.completed",
			CaptureOutcome:      "skipped",
			CaptureReason:       "response_completed_without_usage",
		}
	}

	if s.seenIncomplete {
		modelReturned := bestModelStr(s.incompleteModel, s.createdModel, "", "", "")
		terminalReason := s.incompleteReason
		if terminalReason == "" {
			terminalReason = "incomplete"
		}
		return StreamFinalResult{
			ModelReturned:       modelReturned,
			ModelReturnedSource: "response_incomplete",
			TerminalEvent:       "response.incomplete",
			TerminalReason:      terminalReason,
			CaptureOutcome:      "skipped",
			CaptureReason:       "response_incomplete",
		}
	}

	if s.seenFailed || s.seenErrorEvent {
		modelReturned := bestModelStr(s.failedModel, s.createdModel, "", "", "")
		terminalEvent := "response.failed"
		errInfo := (*ErrorInfo)(nil)
		if s.seenErrorEvent {
			terminalEvent = "error"
			errInfo = &ErrorInfo{
				Class: "response_error_event",
				Type:  s.errorType,
				Code:  s.errorCode,
				Param: s.errorParam,
			}
		}
		return StreamFinalResult{
			ModelReturned:       modelReturned,
			ModelReturnedSource: "response_failed",
			TerminalEvent:       terminalEvent,
			CaptureOutcome:      "failed",
			CaptureReason:       "response_error_event",
			ErrorInfo:           errInfo,
		}
	}

	if s.seenCreated {
		return StreamFinalResult{
			ModelReturned:       s.createdModel,
			ModelReturnedSource: "response_created",
			TerminalEvent:       "stream_end",
			CaptureOutcome:      "skipped",
			CaptureReason:       "stream_ended_without_completed",
		}
	}

	return StreamFinalResult{
		CaptureOutcome: "skipped",
		CaptureReason:  "usage_not_present",
	}
}

func bestModelStr(candidates ...string) string {
	for _, c := range candidates {
		if c != "" {
			return c
		}
	}
	return ""
}

func parseResponsesUsageFromCompleted(model string, raw json.RawMessage) (*UsageInfo, error) {
	var usage responsesUsage
	if err := json.Unmarshal(raw, &usage); err != nil {
		return nil, err
	}
	if usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.TotalTokens == 0 {
		return nil, nil
	}
	info := responsesUsageToInfo(model, &usage)
	info.UsageRawJSON = rawUsageString(raw)
	return info, nil
}

func (s *ResponsesStreamState) JSONParseErrors() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.jsonParseErrors
}
