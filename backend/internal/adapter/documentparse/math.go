package documentparse

import (
	"context"
	"encoding/xml"
	"strings"
)

type mathNode struct {
	name       xml.Name
	attributes []xml.Attr
	text       string
	children   []mathNode
}

func mathName(name xml.Name) bool {
	return name.Space == "http://schemas.openxmlformats.org/officeDocument/2006/math" || name.Space == "http://purl.oclc.org/ooxml/officeDocument/math"
}

func wordMath(ctx context.Context, decoder *xml.Decoder, start xml.StartElement, depth int) (string, error) {
	nodes := 0
	node, err := readMathNode(ctx, decoder, start, depth, &nodes)
	if err != nil {
		return "", err
	}
	return linearMath(node)
}

func readMathNode(ctx context.Context, decoder *xml.Decoder, start xml.StartElement, depth int, nodes *int) (mathNode, error) {
	*nodes++
	if depth > 256 || *nodes > 100_000 {
		return mathNode{}, parseError("DOCUMENT_INVALID")
	}
	node := mathNode{name: start.Name, attributes: start.Attr}
	var content strings.Builder
	for {
		if err := ctx.Err(); err != nil {
			return mathNode{}, err
		}
		token, err := decoder.Token()
		if err != nil {
			return mathNode{}, parseError("DOCUMENT_INVALID")
		}
		switch value := token.(type) {
		case xml.Directive:
			return mathNode{}, parseError("DOCUMENT_INVALID")
		case xml.StartElement:
			child, err := readMathNode(ctx, decoder, value, depth+1, nodes)
			if err != nil {
				return mathNode{}, err
			}
			node.children = append(node.children, child)
		case xml.CharData:
			content.Write(value)
			if content.Len() > MaxCharacters*4 {
				return mathNode{}, parseError("DOCUMENT_CHARACTER_LIMIT")
			}
		case xml.EndElement:
			node.text = content.String()
			return node, nil
		}
	}
}

// OMML structure must survive extraction: concatenating m:t would turn a/b into ab.
func linearMath(node mathNode) (string, error) {
	name := node.name.Local
	if strings.HasSuffix(name, "Pr") && (mathName(node.name) || wordName(node.name)) {
		return "", nil
	}
	if !mathName(node.name) {
		return "", parseError("DOCUMENT_MATH_UNSUPPORTED")
	}
	if name == "t" {
		return node.text, nil
	}
	parts := make(map[string][]string)
	var all []string
	for _, child := range node.children {
		value, err := linearMath(child)
		if err != nil {
			return "", err
		}
		parts[child.name.Local] = append(parts[child.name.Local], value)
		all = append(all, value)
	}
	get := func(key string) string { return strings.Join(parts[key], "") }
	var result string
	switch name {
	case "oMath", "oMathPara", "r", "e", "num", "den", "sub", "sup", "deg", "fName", "lim":
		result = strings.Join(all, "")
	case "f":
		if mathProperty(node, "fPr", "type", "bar") == "noBar" {
			return "", parseError("DOCUMENT_MATH_UNSUPPORTED")
		}
		if get("num") == "" || get("den") == "" {
			return "", parseError("DOCUMENT_MATH_UNSUPPORTED")
		}
		result = "(" + get("num") + ")/(" + get("den") + ")"
	case "sSup":
		result = "(" + get("e") + ")^(" + get("sup") + ")"
	case "sSub":
		result = "(" + get("e") + ")_(" + get("sub") + ")"
	case "sSubSup":
		result = "(" + get("e") + ")_(" + get("sub") + ")^(" + get("sup") + ")"
	case "rad":
		if get("deg") == "" {
			result = "sqrt(" + get("e") + ")"
		} else {
			result = "root(" + get("deg") + "," + get("e") + ")"
		}
	case "nary":
		symbol := mathProperty(node, "naryPr", "chr", "\u222b")
		result = symbol + "_(" + get("sub") + ")^(" + get("sup") + ") (" + get("e") + ")"
	case "d":
		result = mathProperty(node, "dPr", "begChr", "(") + strings.Join(parts["e"], mathProperty(node, "dPr", "sepChr", "|")) + mathProperty(node, "dPr", "endChr", ")")
	case "func":
		result = get("fName") + "(" + get("e") + ")"
	case "limLow":
		result = "(" + get("e") + ")_(" + get("lim") + ")"
	case "limUpp":
		result = "(" + get("e") + ")^(" + get("lim") + ")"
	case "mr":
		result = strings.Join(parts["e"], ",")
	case "m":
		result = "[" + strings.Join(parts["mr"], ";") + "]"
	case "eqArr":
		result = "[" + strings.Join(parts["e"], ";") + "]"
	case "bar":
		result = "overbar(" + get("e") + ")"
		if mathProperty(node, "barPr", "pos", "top") == "bot" {
			result = "underbar(" + get("e") + ")"
		}
	case "acc":
		result = "accent(" + mathProperty(node, "accPr", "chr", "\u0302") + "," + get("e") + ")"
	case "box", "borderBox":
		result = get("e")
	default:
		return "", parseError("DOCUMENT_MATH_UNSUPPORTED")
	}
	if len(result) > MaxCharacters*4 {
		return "", parseError("DOCUMENT_CHARACTER_LIMIT")
	}
	return result, nil
}

func mathProperty(node mathNode, property, key, fallback string) string {
	for _, child := range node.children {
		if !mathName(child.name) || child.name.Local != property {
			continue
		}
		for _, value := range child.children {
			if !mathName(value.name) || value.name.Local != key {
				continue
			}
			for _, attribute := range value.attributes {
				if mathName(attribute.Name) && attribute.Name.Local == "val" {
					return attribute.Value
				}
			}
		}
	}
	return fallback
}
