package aipipeline

// arrayParser is a single-pass streaming parser that extracts complete
// JSON object elements of one named array property from an arriving byte stream.
// It's a handful of states — not a general JSON parser.
// States: waiting-for-array → inside-array → inside-element → back-to-array.
type arrayParser struct {
	field     string       // field is the array property name we care about
	onElement func([]byte) // onElement fires once per complete top-level element bytes

	buf      []byte // buf accumulates bytes of the current element
	state    parserState
	depth    int    // depth tracks { } nesting inside the current element
	inString bool   // inString means the cursor is inside a JSON string literal
	escape   bool   // escape means the previous byte was a backslash inside a string
	fieldBuf []byte // fieldBuf accumulates property names while matching `field`
}

// parserState identifies where the parser is in the input.
type parserState int

const (
	// stateSearchField is looking for the target property name in the outer object.
	stateSearchField parserState = iota
	// stateAwaitArray has matched the property name and is waiting for '['.
	stateAwaitArray
	// stateInsideArray is between elements, waiting for '{' (element start) or ']' (array end).
	stateInsideArray
	// stateInsideElement collects one element's bytes until depth returns to 0.
	stateInsideElement
	// stateDone means the array closed; parser ignores further input.
	stateDone
)

// newArrayParser returns a parser that extracts elements of the named array.
func newArrayParser(field string) *arrayParser {
	return &arrayParser{field: field}
}

// feed pushes bytes into the parser, firing onElement for each closed element.
func (p *arrayParser) feed(b []byte) {
	for _, c := range b {
		p.step(c)
	}
}

// step advances the parser by one byte.
func (p *arrayParser) step(c byte) {
	switch p.state {
	case stateSearchField:
		p.searchField(c)
	case stateAwaitArray:
		p.awaitArray(c)
	case stateInsideArray:
		p.insideArray(c)
	case stateInsideElement:
		p.insideElement(c)
	}
}

// searchField accumulates the current property name and transitions when matched.
func (p *arrayParser) searchField(c byte) {
	if p.inString {
		p.appendFieldChar(c)
		return
	}
	if c == '"' {
		p.inString = true
		p.fieldBuf = p.fieldBuf[:0]
		return
	}
	if c == ':' && string(p.fieldBuf) == p.field {
		p.state = stateAwaitArray
	}
}

// appendFieldChar handles one byte while inside a property-name string literal.
func (p *arrayParser) appendFieldChar(c byte) {
	if p.escape {
		p.fieldBuf = append(p.fieldBuf, c)
		p.escape = false
		return
	}
	if c == '\\' {
		p.escape = true
		return
	}
	if c == '"' {
		p.inString = false
		return
	}
	p.fieldBuf = append(p.fieldBuf, c)
}

// awaitArray consumes whitespace and transitions on '['.
func (p *arrayParser) awaitArray(c byte) {
	if c == '[' {
		p.state = stateInsideArray
	}
}

// insideArray begins collecting a new element on '{', or ends on ']'.
func (p *arrayParser) insideArray(c byte) {
	if c == '{' {
		p.buf = append(p.buf[:0], '{')
		p.depth = 1
		p.state = stateInsideElement
		return
	}
	if c == ']' {
		p.state = stateDone
	}
}

// insideElement accumulates bytes until the element's top-level '}' closes.
func (p *arrayParser) insideElement(c byte) {
	p.buf = append(p.buf, c)
	if p.inString {
		p.handleInStringInside(c)
		return
	}
	if c == '"' {
		p.inString = true
		return
	}
	if c == '{' {
		p.depth++
	}
	if c == '}' {
		p.depth--
		if p.depth == 0 {
			p.onElement(p.buf)
			p.buf = p.buf[:0]
			p.state = stateInsideArray
		}
	}
}

// handleInStringInside tracks string/escape state while inside an element.
func (p *arrayParser) handleInStringInside(c byte) {
	if p.escape {
		p.escape = false
		return
	}
	if c == '\\' {
		p.escape = true
		return
	}
	if c == '"' {
		p.inString = false
	}
}
