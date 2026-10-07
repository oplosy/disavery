package runbook

import (
	"fmt"
	"maps"
	"strings"
	"text/template"
)

func parseTemplate(text string) (*template.Template, error) {
	return template.New("").Option("missingkey=error").Parse(text)
}

// Render executes text as a Go template over data. Missing keys are errors.
func Render(text string, data map[string]any) (string, error) {
	if !strings.Contains(text, "{{") {
		return text, nil
	}
	t, err := parseTemplate(text)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	if err := t.Execute(&b, data); err != nil {
		return "", err
	}
	return b.String(), nil
}

// templated lists pointers to every templated string field of s.
func (s *Step) templated() map[string]*string {
	f := map[string]*string{"run": &s.Run, "manual": &s.Manual}
	if s.SSH != nil {
		f["ssh.host"], f["ssh.cmd"] = &s.SSH.Host, &s.SSH.Cmd
	}
	if s.WaitHTTP != nil {
		f["wait_http.url"] = &s.WaitHTTP.URL
	}
	return f
}

// Rendered returns a copy of s with every templated field rendered over data.
func (s Step) Rendered(data map[string]any) (Step, error) {
	if s.SSH != nil {
		c := *s.SSH
		s.SSH = &c
	}
	if s.WaitHTTP != nil {
		c := *s.WaitHTTP
		s.WaitHTTP = &c
	}
	s.With = maps.Clone(s.With)
	for name, p := range s.templated() {
		v, err := Render(*p, data)
		if err != nil {
			return Step{}, fmt.Errorf("%s: %w", name, err)
		}
		*p = v
	}
	for k, v := range s.With {
		r, err := Render(v, data)
		if err != nil {
			return Step{}, fmt.Errorf("with.%s: %w", k, err)
		}
		s.With[k] = r
	}
	return s, nil
}
