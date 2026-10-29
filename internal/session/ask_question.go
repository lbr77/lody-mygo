package session

import (
	"math"
	"strconv"
	"strings"
)

// AskQuestions is what an AskUserQuestion request asks, read from its
// permission request's metadata as Lody's parseAskUserQuestionPermissionMeta
// reads it: Lody's own elicitation (_meta.lody.elicitation, version 1)
// first, then Claude Code's (_meta.claudeCode.askUserQuestion), then
// Codex's (_meta.codex.requestUserInput). The daemon writes Lody's, with
// the agent's own beside it for older clients.
type AskQuestions struct {
	Source            string // lody, claude or codex: the shape answers go back in
	Version           int
	AllowCustomAnswer bool
	Questions         []AskQuestion
	// AutoResolveAt is when the agent goes on without an answer, in Unix
	// milliseconds; zero while it waits.
	AutoResolveAt float64
}

// AskQuestion is one question of a request.
type AskQuestion struct {
	ID, Question, Header string
	Options              []AskOption
	MultiSelect          bool
	// AllowCustomAnswer, when set, says whether the question takes an
	// answer of the user's own; unset, the request's AllowCustomAnswer says.
	AllowCustomAnswer *bool
	IsSecret          bool
	Note              *AskNote
}

// AskOption is a choice a question offers: its label, which is also the
// answer, and what its details show.
type AskOption struct {
	Label, Description, Preview string
}

// AskNote is the free text a question takes beside its answer, sent
// under its own field id.
type AskNote struct {
	FieldID, Title, Description string
	IsSecret                    bool
}

// AskAnswers are a request's answers by answer key (AskAnswerKey): a
// string, or a []string of a multi-select question's labels; and each
// note given, by its field id, as a string.
type AskAnswers map[string]any

// CustomAnswer reports whether the question takes an answer of the user's
// own, as Lody's question?.allowCustomAnswer ?? meta.allowCustomAnswer.
func (qs *AskQuestions) CustomAnswer(i int) bool {
	if q := qs.Questions[i]; q.AllowCustomAnswer != nil {
		return *q.AllowCustomAnswer
	}
	return qs.AllowCustomAnswer
}

// ParseAskQuestions reads a permission request's metadata; nil when it is
// no question request, or a malformed one.
func ParseAskQuestions(meta map[string]any) *AskQuestions {
	if lody := obj(meta["lody"]); lody != nil {
		if qs := parseLodyElicitation(lody); qs != nil {
			return qs
		}
		// A request in Lody's shape that does not read is no question,
		// whatever the agent's own copy says.
		if el := obj(lody["elicitation"]); el != nil && isNumber(el["version"], 1) {
			return nil
		}
	}
	if cc := obj(meta["claudeCode"]); cc != nil {
		return parseClaudeQuestions(cc)
	}
	if cx := obj(meta["codex"]); cx != nil {
		return parseCodexQuestions(cx)
	}
	return nil
}

func parseLodyElicitation(lody map[string]any) *AskQuestions {
	raw := obj(lody["elicitation"])
	rawQuestions, ok := raw["questions"].([]any)
	if raw == nil || !isNumber(raw["version"], 1) || !ok {
		return nil
	}
	questions := parsePermissionQuestions(rawQuestions)
	if questions == nil {
		return nil
	}
	qs := &AskQuestions{Source: "lody", Version: 1, Questions: questions}
	for _, q := range questions {
		qs.AllowCustomAnswer = qs.AllowCustomAnswer || q.AllowCustomAnswer != nil && *q.AllowCustomAnswer
	}
	if at, ok := finite(raw["autoResolveAtEpochSeconds"]); ok {
		qs.AutoResolveAt = at * 1000
	} else if at, ok := finite(raw["autoResolveAt"]); ok {
		// The millisecond field before Lody's Core metadata, for a release.
		qs.AutoResolveAt = at
	}
	return qs
}

func parsePermissionQuestions(raw []any) []AskQuestion {
	if len(raw) == 0 {
		return nil
	}
	var questions []AskQuestion
	for _, r := range raw {
		rq := obj(r)
		question, okQ := rq["question"].(string)
		header, okH := rq["header"].(string)
		rawOptions, okO := rq["options"].([]any)
		if rq == nil || !okQ || !okH || !okO {
			return nil
		}
		q := AskQuestion{Question: question, Header: header, MultiSelect: rq["multiSelect"] == true, IsSecret: rq["isSecret"] == true}
		if note, set := rq["note"]; set {
			n := obj(note)
			fieldID, ok := n["fieldId"].(string)
			if n == nil || !ok || strings.TrimSpace(fieldID) == "" || !optionalString(n["title"]) || !optionalString(n["description"]) || !optionalBool(n["isSecret"]) {
				return nil
			}
			q.Note = &AskNote{FieldID: fieldID, Title: str(n["title"]), Description: str(n["description"]), IsSecret: n["isSecret"] == true}
		}
		for _, o := range rawOptions {
			opt := obj(o)
			label, ok := opt["label"].(string)
			if opt == nil || !ok {
				return nil
			}
			q.Options = append(q.Options, AskOption{Label: label, Description: str(opt["description"]), Preview: str(opt["preview"])})
		}
		if id, ok := rq["id"].(string); ok {
			q.ID = id
		}
		if v, ok := rq["allowCustomAnswer"].(bool); ok {
			q.AllowCustomAnswer = &v
		}
		questions = append(questions, q)
	}
	if !validNoteKeys(questions) {
		return nil
	}
	return questions
}

// validNoteKeys reports whether no note's field id shadows a question or
// another note: a request with notes names every question by a unique id.
func validNoteKeys(questions []AskQuestion) bool {
	notes := false
	for _, q := range questions {
		notes = notes || q.Note != nil
	}
	if !notes {
		return true
	}
	used := map[string]bool{}
	for _, q := range questions {
		if strings.TrimSpace(q.ID) == "" || used[q.ID] {
			return false
		}
		used[q.ID] = true
	}
	for _, q := range questions {
		if q.Note == nil {
			continue
		}
		if strings.TrimSpace(q.Note.FieldID) == "" || used[q.Note.FieldID] {
			return false
		}
		used[q.Note.FieldID] = true
	}
	return true
}

func parseClaudeQuestions(cc map[string]any) *AskQuestions {
	raw := obj(cc["askUserQuestion"])
	rawQuestions, ok := raw["questions"].([]any)
	if raw == nil || !ok || len(rawQuestions) == 0 {
		return nil
	}
	qs := &AskQuestions{Source: "claude", Version: 1, AllowCustomAnswer: raw["allowCustomAnswer"] == true}
	if v, ok := finite(raw["version"]); ok {
		qs.Version = int(v)
	}
	for _, r := range rawQuestions {
		rq := obj(r)
		question, okQ := rq["question"].(string)
		header, okH := rq["header"].(string)
		rawOptions, okO := rq["options"].([]any)
		if rq == nil || !okQ || !okH || !okO {
			return nil
		}
		q := AskQuestion{Question: question, Header: header, MultiSelect: rq["multiSelect"] == true}
		for _, o := range rawOptions {
			opt := obj(o)
			label, ok := opt["label"].(string)
			if opt == nil || !ok {
				return nil
			}
			q.Options = append(q.Options, AskOption{Label: label, Description: str(opt["description"]), Preview: str(opt["preview"])})
		}
		qs.Questions = append(qs.Questions, q)
	}
	return qs
}

func parseCodexQuestions(cx map[string]any) *AskQuestions {
	raw := obj(cx["requestUserInput"])
	rawQuestions, ok := raw["questions"].([]any)
	if raw == nil || !ok || len(rawQuestions) == 0 {
		return nil
	}
	qs := &AskQuestions{Source: "codex", Version: 1}
	for _, r := range rawQuestions {
		rq := obj(r)
		id, okI := rq["id"].(string)
		question, okQ := rq["question"].(string)
		header, okH := rq["header"].(string)
		if rq == nil || !okI || !okQ || !okH {
			return nil
		}
		custom := rq["isOther"] == true || rq["is_other"] == true || rq["allowCustomAnswer"] == true
		q := AskQuestion{ID: id, Question: question, Header: header, AllowCustomAnswer: &custom,
			IsSecret: rq["isSecret"] == true || rq["is_secret"] == true}
		if rawOptions, set := rq["options"]; set {
			options, ok := rawOptions.([]any)
			if !ok {
				return nil
			}
			for _, o := range options {
				opt := obj(o)
				label, ok := opt["label"].(string)
				if opt == nil || !ok {
					return nil
				}
				q.Options = append(q.Options, AskOption{Label: label, Description: str(opt["description"])})
			}
		}
		qs.AllowCustomAnswer = qs.AllowCustomAnswer || custom
		qs.Questions = append(qs.Questions, q)
	}
	if at, ok := finite(raw["autoResolveAt"]); ok {
		qs.AutoResolveAt = at
	}
	return qs
}

// AskAnswerKey is the key a question's answer goes under, as Lody's
// getAskUserQuestionAnswerKey: its id, else its question, else its header,
// whichever is first unique and not blank among the questions; else its
// index.
func AskAnswerKey(questions []AskQuestion, i int) string {
	if i < 0 || i >= len(questions) {
		return strconv.Itoa(i)
	}
	q := questions[i]
	unique := func(value string, of func(AskQuestion) string) bool {
		if strings.TrimSpace(value) == "" {
			return false
		}
		n := 0
		for _, other := range questions {
			if of(other) == value {
				n++
			}
		}
		return n == 1
	}
	switch {
	case q.ID != "" && unique(q.ID, func(o AskQuestion) string { return o.ID }):
		return q.ID
	case unique(q.Question, func(o AskQuestion) string { return o.Question }):
		return q.Question
	case unique(q.Header, func(o AskQuestion) string { return o.Header }):
		return q.Header
	}
	return strconv.Itoa(i)
}

// AskOutcome is the outcome that answers a request with optionID, its
// answers in the request's own shape, as Lody's
// createAskUserQuestionPermissionOutcome: under _meta.lody.elicitation for
// Lody's, _meta.codex.requestUserInput (each answer a list) for Codex's,
// and _meta.claudeCode.askUserQuestion for Claude's.
func AskOutcome(optionID string, answers AskAnswers, source string) map[string]any {
	var meta map[string]any
	switch source {
	case "lody":
		meta = map[string]any{"lody": map[string]any{"elicitation": map[string]any{"version": float64(1), "answers": answerValues(answers)}}}
	case "codex":
		codex := map[string]any{}
		for k, v := range answers {
			values, ok := v.([]string)
			if !ok {
				values = []string{str(v)}
			}
			codex[k] = map[string]any{"answers": stringList(values)}
		}
		meta = map[string]any{"codex": map[string]any{"requestUserInput": map[string]any{"answers": codex}}}
	default:
		meta = map[string]any{"claudeCode": map[string]any{"askUserQuestion": map[string]any{"answers": answerValues(answers)}}}
	}
	return map[string]any{"outcome": "selected", "optionId": optionID, "_meta": meta}
}

// answerValues are the answers as the document holds them.
func answerValues(answers AskAnswers) map[string]any {
	out := map[string]any{}
	for k, v := range answers {
		if values, ok := v.([]string); ok {
			out[k] = stringList(values)
			continue
		}
		out[k] = v
	}
	return out
}

func stringList(values []string) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = v
	}
	return out
}

// ExtractAskAnswers reads the answers a request's outcome carries, as
// Lody's extractAskUserQuestionAnswersFromOutcome: Lody's shape first, then
// Codex's, then Claude's, whichever the request asked in, keyed back onto
// the request's answer keys. Nil when it carries none, as a cancel.
func ExtractAskAnswers(qs *AskQuestions, outcome map[string]any) AskAnswers {
	meta := obj(outcome["_meta"])
	if qs == nil || meta == nil || !validNoteKeys(qs.Questions) {
		return nil
	}
	source, raw := "", map[string]any(nil)
	if a := obj(obj(obj(meta["lody"])["elicitation"])["answers"]); a != nil {
		source, raw = "lody", a
	} else if a := obj(obj(obj(meta["codex"])["requestUserInput"])["answers"]); a != nil {
		source, raw = "codex", a
	} else if a := obj(obj(obj(meta["claudeCode"])["askUserQuestion"])["answers"]); a != nil {
		source, raw = "claude", a
	} else {
		return nil
	}
	// Claude's older answers were keyed without the Core field ids,
	// normally by question.
	unnamed := make([]AskQuestion, len(qs.Questions))
	for i, q := range qs.Questions {
		q.ID = ""
		unnamed[i] = q
	}
	out := AskAnswers{}
	for i, q := range qs.Questions {
		if q.Note != nil && source == "lody" {
			if note, ok := raw[q.Note.FieldID].(string); ok && strings.TrimSpace(note) != "" {
				out[q.Note.FieldID] = note
			}
		}
		key := AskAnswerKey(qs.Questions, i)
		from := key
		if source == "claude" {
			from = AskAnswerKey(unnamed, i)
		}
		v, ok := raw[from]
		if !ok || v == nil {
			continue
		}
		if source == "codex" {
			inner, ok := obj(v)["answers"].([]any)
			if !ok {
				continue
			}
			if values := stringsOf(inner); len(values) > 0 {
				out[key] = oneOrMany(q, values)
			}
			continue
		}
		switch x := v.(type) {
		case string:
			out[key] = x
		case []any:
			if values := stringsOf(x); len(values) > 0 {
				out[key] = oneOrMany(q, values)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// oneOrMany is a multi-select question's labels, or the first of another's.
func oneOrMany(q AskQuestion, values []string) any {
	if q.MultiSelect {
		return values
	}
	return values[0]
}

// stringsOf are the strings of a list.
func stringsOf(l []any) []string {
	var out []string
	for _, v := range l {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func finite(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int64:
		return float64(n), true
	}
	return 0, false
}

func isNumber(v any, want float64) bool {
	n, ok := finite(v)
	return ok && n == want
}

func optionalString(v any) bool {
	_, ok := v.(string)
	return v == nil || ok
}

func optionalBool(v any) bool {
	_, ok := v.(bool)
	return v == nil || ok
}
