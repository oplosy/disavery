package runbook

import (
	"errors"
	"fmt"
	"strings"
)

// Expr is a parsed `when` condition: comparisons of a variable with a quoted
// string, joined by && and ||, e.g. "tier == 'pilot-light' && site != 'b'".
// && binds tighter than ||; there are no parentheses.
type Expr struct {
	src string
	or  [][]comparison
}

type comparison struct {
	ident, value string
	negate       bool
}

type tokenKind int

const (
	tIdent tokenKind = iota
	tString
	tEq
	tNe
	tAnd
	tOr
)

type token struct {
	kind tokenKind
	text string
}

// ParseExpr parses a condition.
func ParseExpr(src string) (*Expr, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, fmt.Errorf("condition %q: %w", src, err)
	}
	if len(toks) == 0 {
		return nil, errors.New("empty condition")
	}
	e := &Expr{src: src}
	var group []comparison
	for i := 0; ; {
		if i+3 > len(toks) || toks[i].kind != tIdent ||
			(toks[i+1].kind != tEq && toks[i+1].kind != tNe) || toks[i+2].kind != tString {
			return nil, fmt.Errorf("condition %q: expected <variable> == '<value>' or <variable> != '<value>'", src)
		}
		group = append(group, comparison{ident: toks[i].text, value: toks[i+2].text, negate: toks[i+1].kind == tNe})
		i += 3
		if i == len(toks) {
			break
		}
		switch toks[i].kind {
		case tAnd:
		case tOr:
			e.or = append(e.or, group)
			group = nil
		default:
			return nil, fmt.Errorf("condition %q: expected && or || after a comparison", src)
		}
		i++
	}
	e.or = append(e.or, group)
	return e, nil
}

func tokenize(s string) ([]token, error) {
	var toks []token
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t':
			i++
		case c == '\'':
			end := strings.IndexByte(s[i+1:], '\'')
			if end < 0 {
				return nil, errors.New("unterminated string")
			}
			toks = append(toks, token{tString, s[i+1 : i+1+end]})
			i += end + 2
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && (isIdentStart(s[j]) || (s[j] >= '0' && s[j] <= '9')) {
				j++
			}
			toks = append(toks, token{tIdent, s[i:j]})
			i = j
		case strings.HasPrefix(s[i:], "=="):
			toks = append(toks, token{kind: tEq})
			i += 2
		case strings.HasPrefix(s[i:], "!="):
			toks = append(toks, token{kind: tNe})
			i += 2
		case strings.HasPrefix(s[i:], "&&"):
			toks = append(toks, token{kind: tAnd})
			i += 2
		case strings.HasPrefix(s[i:], "||"):
			toks = append(toks, token{kind: tOr})
			i += 2
		default:
			return nil, fmt.Errorf("unexpected character %q", c)
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool { return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }

// Eval evaluates the condition. Referring to an unknown variable is an error.
func (e *Expr) Eval(vars map[string]string) (bool, error) {
	for _, and := range e.or {
		ok := true
		for _, c := range and {
			v, found := vars[c.ident]
			if !found {
				return false, fmt.Errorf("condition %q: unknown variable %q", e.src, c.ident)
			}
			if (v == c.value) == c.negate {
				ok = false
				break
			}
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Idents returns the variables the condition refers to.
func (e *Expr) Idents() []string {
	var out []string
	for _, and := range e.or {
		for _, c := range and {
			out = append(out, c.ident)
		}
	}
	return out
}
