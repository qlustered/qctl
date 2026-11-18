package guide

import (
	"slices"
	"sort"
	"strings"

	"github.com/qlustered/qctl/internal/pkg/fuzzy"
)

// maxTopicSuggestions caps the topics an unknown name is answered with.
const maxTopicSuggestions = 3

// section is one heading of a topic and the lines under it, up to the next
// heading of the same or a higher level.
type section struct {
	Heading string // without the leading #s
	Level   int
	Body    string // the heading line included
}

// sections splits body at its markdown headings. Lines inside a fenced code
// block are never headings, so a quoted "# rules.py" stays in its section.
// Text before the first heading is not a section.
func sections(body string) []section {
	lines := strings.Split(body, "\n")
	type mark struct{ line, level int }
	var marks []mark
	fence := ""
	for i, line := range lines {
		trimmed := strings.TrimLeft(line, " ")
		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fence = trimmed[:3]
			continue
		}
		if level := headingLevel(line); level > 0 {
			marks = append(marks, mark{i, level})
		}
	}
	out := make([]section, 0, len(marks))
	for k, m := range marks {
		end := len(lines)
		for _, next := range marks[k+1:] {
			if next.level <= m.level {
				end = next.line
				break
			}
		}
		out = append(out, section{
			Heading: strings.TrimSpace(strings.TrimLeft(lines[m.line], "#")),
			Level:   m.level,
			Body:    strings.TrimRight(strings.Join(lines[m.line:end], "\n"), "\n") + "\n",
		})
	}
	return out
}

// headingLevel returns the ATX heading level of line, or 0 when it is not a
// heading.
func headingLevel(line string) int {
	n := len(line) - len(strings.TrimLeft(line, "#"))
	if n == 0 || n > 6 || len(line) == n || line[n] != ' ' {
		return 0
	}
	return n
}

// findSections returns the sections of body whose heading contains query,
// case-insensitively. A section nested inside an earlier match is left out,
// because the earlier match already prints it.
func findSections(body, query string) []section {
	q := strings.ToLower(strings.TrimSpace(query))
	var out []section
	for _, s := range sections(body) {
		if !strings.Contains(strings.ToLower(s.Heading), q) {
			continue
		}
		if len(out) > 0 && strings.Contains(out[len(out)-1].Body, s.Body) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// words splits s into lower-case words of three or more letters, with a
// trailing "s" dropped so "alerts" and "alert" meet.
func words(s string) []string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	out := fields[:0]
	for _, f := range fields {
		if len(f) < 3 {
			continue
		}
		if len(f) > 3 && strings.HasSuffix(f, "s") && !strings.HasSuffix(f, "ss") {
			f = f[:len(f)-1]
		}
		out = append(out, f)
	}
	return out
}

// suggestTopics returns up to three topics close to name: near misses of a
// topic name or alias first, then the topics whose name or summary shares the
// most words with it.
func suggestTopics(name string) []topic {
	var candidates []fuzzy.Candidate
	for _, t := range topics {
		candidates = append(candidates, fuzzy.Candidate{Match: t.Name, Value: t.Name})
		for _, a := range t.Aliases {
			candidates = append(candidates, fuzzy.Candidate{Match: a, Value: t.Name})
		}
	}
	picked := fuzzy.SuggestFromN(name, candidates, 0)

	query := words(name)
	type scored struct {
		name  string
		score int
	}
	var byWords []scored
	for _, t := range topics {
		have := map[string]bool{}
		for _, w := range words(t.Name + " " + strings.Join(t.Aliases, " ") + " " + t.Summary) {
			have[w] = true
		}
		score := 0
		for _, w := range query {
			if have[w] {
				score++
			}
		}
		if score > 0 {
			byWords = append(byWords, scored{t.Name, score})
		}
	}
	sort.SliceStable(byWords, func(i, j int) bool { return byWords[i].score > byWords[j].score })
	for _, s := range byWords {
		if !slices.Contains(picked, s.name) {
			picked = append(picked, s.name)
		}
	}

	var out []topic
	for _, n := range picked {
		if t, ok := lookup(n); ok && len(out) < maxTopicSuggestions {
			out = append(out, t)
		}
	}
	return out
}

// searchHit is one topic matching a search, with the headings of the
// sections the query appears in.
type searchHit struct {
	Topic    topic
	Count    int
	Sections []string
}

// search returns the topics whose body or summary contains every word of
// query, case-insensitively, most occurrences first.
func search(query string) ([]searchHit, error) {
	terms := strings.Fields(strings.ToLower(query))
	if len(terms) == 0 {
		return nil, nil
	}
	var hits []searchHit
	for _, t := range topics {
		body, err := t.body()
		if err != nil {
			return nil, err
		}
		text := strings.ToLower(t.Summary + "\n" + body)
		count := 0
		for _, term := range terms {
			n := strings.Count(text, term)
			if n == 0 {
				count = 0
				break
			}
			count += n
		}
		if count == 0 {
			continue
		}
		hit := searchHit{Topic: t, Count: count}
		for _, s := range sections(body) {
			if containsAll(strings.ToLower(ownText(s)), terms) {
				hit.Sections = append(hit.Sections, s.Heading)
			}
		}
		hits = append(hits, hit)
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].Count > hits[j].Count })
	return hits, nil
}

// ownText is a section's text without the subsections nested in it.
func ownText(s section) string {
	subs := sections(s.Body)
	if len(subs) < 2 {
		return s.Body
	}
	first, _, _ := strings.Cut(subs[1].Body, "\n")
	own, _, _ := strings.Cut(s.Body, "\n"+first+"\n")
	return own
}

func containsAll(text string, terms []string) bool {
	for _, t := range terms {
		if !strings.Contains(text, t) {
			return false
		}
	}
	return true
}
