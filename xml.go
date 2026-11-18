package udf

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

type xmlLimits struct {
	depth     int
	nodes     int
	textBytes int64
	attrBytes int64
}

type xmlNode struct {
	Name        xml.Name
	Attr        map[string]string
	Text        strings.Builder
	Children    []*xmlNode
	Raw         []byte
	sourceStart int64
}

func child(n *xmlNode, name string) *xmlNode {
	if n == nil {
		return nil
	}
	for _, c := range n.Children {
		if c.Name.Local == name {
			return c
		}
	}
	return nil
}

func children(n *xmlNode, name string) []*xmlNode {
	var out []*xmlNode
	if n != nil {
		for _, c := range n.Children {
			if c.Name.Local == name {
				out = append(out, c)
			}
		}
	}
	return out
}

func attr(n *xmlNode, name string) string {
	if n == nil {
		return ""
	}
	return n.Attr[name]
}

func decodeTree(data []byte, limits xmlLimits) (*xmlNode, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	stack := make([]*xmlNode, 0, 16)
	var root *xmlNode
	var nodes int
	var textBytes, attrBytes int64
	for {
		tokenStart := dec.InputOffset()
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("udf: %w", ErrInvalidXML)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			nodes++
			if nodes > limits.nodes || len(stack)+1 > limits.depth {
				return nil, ErrLimitExceeded
			}
			n := &xmlNode{Name: t.Name, Attr: make(map[string]string, len(t.Attr)), sourceStart: tokenStart}
			for _, a := range t.Attr {
				key := a.Name.Local
				if _, exists := n.Attr[key]; exists {
					return nil, fmt.Errorf("udf: duplicate XML attribute: %w", ErrInvalidXML)
				}
				attrBytes += int64(len(a.Value))
				if attrBytes > limits.attrBytes {
					return nil, ErrLimitExceeded
				}
				n.Attr[key] = a.Value
			}
			if len(stack) > 0 {
				parent := stack[len(stack)-1]
				parent.Children = append(parent.Children, n)
			} else if root == nil {
				root = n
			} else {
				return nil, fmt.Errorf("udf: multiple XML roots: %w", ErrInvalidXML)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if len(stack) == 0 || stack[len(stack)-1].Name != t.Name {
				return nil, fmt.Errorf("udf: malformed XML nesting: %w", ErrInvalidXML)
			}
			n := stack[len(stack)-1]
			end := dec.InputOffset()
			if end >= tokenStart && end <= int64(len(data)) {
				// StartOffset is recovered from the node's token start marker.
				if n.sourceStart >= 0 && n.sourceStart <= end {
					n.Raw = data[n.sourceStart:end]
				}
			}
			stack = stack[:len(stack)-1]
		case xml.CharData:
			textBytes += int64(len(t))
			if textBytes > limits.textBytes {
				return nil, ErrLimitExceeded
			}
			if len(stack) > 0 {
				stack[len(stack)-1].Text.Write([]byte(t))
			} else if strings.TrimSpace(string(t)) != "" {
				return nil, fmt.Errorf("udf: text outside XML root: %w", ErrInvalidXML)
			}
		}
	}
	if root == nil || len(stack) != 0 {
		return nil, fmt.Errorf("udf: incomplete XML: %w", ErrInvalidXML)
	}
	return root, nil
}

func nodeRaw(n *xmlNode) []byte {
	if n == nil || len(n.Raw) == 0 {
		return nil
	}
	return append([]byte(nil), n.Raw...)
}
