package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"
)

// ConfigTokenType is an enum of token types the config scanner can produce
type ConfigTokenType uint

// Token types produced by the config scanner
const (
	CtkInvalid ConfigTokenType = 1 << iota
	CtkWord
	CtkOption
	CtkWhiteSpace
	CtkComment
	CtkShellCommand
	CtkTerminator
	CtkEOF

	CtkCount
)

var configTokenNames = map[ConfigTokenType]string{
	CtkInvalid:      "Invalid",
	CtkWord:         "Word",
	CtkOption:       "Option",
	CtkWhiteSpace:   "White Space",
	CtkComment:      "Comment",
	CtkShellCommand: "Shell Command",
	CtkTerminator:   "Terminator",
	CtkEOF:          "EOF",
}

// ConfigScannerPos represents a position in the config scanner input stream
type ConfigScannerPos struct {
	line uint
	col  uint
}

// ConfigToken is a config token parsed from an input stream
// It contains position, error and value data
type ConfigToken struct {
	tokenType ConfigTokenType
	value     string
	startPos  ConfigScannerPos
	endPos    ConfigScannerPos
	err       error
}

// ConfigScanner scans an input stream and generates a stream of config tokens
type ConfigScanner struct {
	reader          *bufio.Reader
	pos             ConfigScannerPos
	lastCharLineEnd bool
	lastLineEndCol  uint
}

// Equal returns true if the other token is equal
func (token *ConfigToken) Equal(other *ConfigToken) bool {
	if other == nil {
		return false
	}

	return token.tokenType == other.tokenType &&
		token.value == other.value &&
		token.startPos == other.startPos &&
		token.endPos == other.endPos &&
		((token.err == nil && other.err == nil) ||
			(token.err != nil && other.err != nil &&
				token.err.Error() == other.err.Error()))
}

// ConfigTokenName maps token types to human readable names
func ConfigTokenName(tokenType ConfigTokenType) string {
	tokens := []string{}

	for i := CtkInvalid; i < CtkCount; i <<= 1 {
		if (i & tokenType) != 0 {
			tokens = append(tokens, configTokenNames[i])
		}
	}

	return strings.Join(tokens, " or ")
}

// NewConfigScanner creates a new scanner which uses the provided reader
func NewConfigScanner(reader io.Reader) *ConfigScanner {
	return &ConfigScanner{
		reader: bufio.NewReader(reader),
		pos: ConfigScannerPos{
			line: 1,
			col:  0,
		},
	}
}

func (scanner *ConfigScanner) read() (char rune, eof bool, err error) {
	char, _, err = scanner.reader.ReadRune()

	if err == io.EOF {
		eof = true
		err = nil

		if scanner.pos.col == 0 {
			scanner.pos.col = 1
		}
	} else if err == nil {
		if scanner.lastCharLineEnd {
			scanner.lastLineEndCol = scanner.pos.col
			scanner.pos.line++
			scanner.pos.col = 1
		} else {
			scanner.pos.col++
		}

		scanner.lastCharLineEnd = (char == '\n')
	}

	return
}

func (scanner *ConfigScanner) unread() (err error) {
	if err = scanner.reader.UnreadRune(); err != nil {
		return
	}

	if scanner.pos.line > 1 && scanner.pos.col == 1 {
		scanner.pos.line--
		scanner.pos.col = scanner.lastLineEndCol
		scanner.lastCharLineEnd = true
	} else {
		scanner.pos.col--
		scanner.lastCharLineEnd = false
	}

	return
}

// Scan returns the next token from the input stream
func (scanner *ConfigScanner) Scan() (token *ConfigToken, err error) {
	char, eof, err := scanner.read()
	startPos := scanner.pos

	switch {
	case err != nil:
	case eof:
		token = &ConfigToken{
			tokenType: CtkEOF,
			endPos:    scanner.pos,
		}
	case char == '\n':
		token = &ConfigToken{
			tokenType: CtkTerminator,
			value:     string(char),
			endPos:    scanner.pos,
		}
	case unicode.IsSpace(char):
		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanWhiteSpace()
	case char == '#':
		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanComment()
	case char == '!' || char == '@':
		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanShellCommand()
	case char == '-':
		var nextBytes []byte
		nextBytes, err = scanner.reader.Peek(1)

		if err != nil {
			break
		} else if len(nextBytes) == 1 && nextBytes[0] == '-' {
			token, err = scanner.scanWord()

			if token != nil && token.tokenType != CtkInvalid {
				token.tokenType = CtkOption
				token.value = "-" + token.value
			}

			break
		}

		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanWord()
	case char == '"':
		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanStringWord()
	default:
		if err = scanner.unread(); err != nil {
			break
		}

		token, err = scanner.scanWord()
	}

	if token != nil {
		token.startPos = startPos
	}

	return
}

func (scanner *ConfigScanner) scanWhiteSpace() (token *ConfigToken, err error) {
	var buffer bytes.Buffer
	var char rune
	var eof bool

	escape := false

OuterLoop:
	for {
		char, eof, err = scanner.read()

		switch {
		case err != nil:
			return
		case eof:
			break OuterLoop
		case char == '\\':
			var nextBytes []byte
			nextBytes, err = scanner.reader.Peek(1)

			if err != nil {
				return
			} else if len(nextBytes) == 1 && nextBytes[0] == '\n' {
				escape = true
				continue
			}

			if err = scanner.unread(); err != nil {
				return
			}

			break OuterLoop
		case char == '\n':
			if !escape {
				if err = scanner.unread(); err != nil {
					return
				}

				break OuterLoop
			}
		case !unicode.IsSpace(char):
			if err = scanner.unread(); err != nil {
				return
			}

			break OuterLoop
		default:
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}
		}

		escape = false
	}

	token = &ConfigToken{
		tokenType: CtkWhiteSpace,
		value:     buffer.String(),
		endPos:    scanner.pos,
	}

	return
}

func (scanner *ConfigScanner) scanComment() (token *ConfigToken, err error) {
	return scanner.scanToEndOfLine(CtkComment)
}

func (scanner *ConfigScanner) scanShellCommand() (token *ConfigToken, err error) {
	return scanner.scanToEndOfLine(CtkShellCommand)
}

func (scanner *ConfigScanner) scanToEndOfLine(tokenType ConfigTokenType) (token *ConfigToken, err error) {
	var buffer bytes.Buffer
	var char rune
	var eof bool

OuterLoop:
	for {
		char, eof, err = scanner.read()

		switch {
		case err != nil:
			return
		case eof:
			break OuterLoop
		case char == '\n':
			if err = scanner.unread(); err != nil {
				return
			}

			break OuterLoop
		default:
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}
		}
	}

	token = &ConfigToken{
		tokenType: tokenType,
		value:     buffer.String(),
		endPos:    scanner.pos,
	}

	return
}

func (scanner *ConfigScanner) scanWord() (token *ConfigToken, err error) {
	var buffer bytes.Buffer
	var char rune
	var eof bool

OuterLoop:
	for {
		char, eof, err = scanner.read()

		switch {
		case err != nil:
			return
		case eof:
			break OuterLoop
		case unicode.IsSpace(char):
			if err = scanner.unread(); err != nil {
				return
			}

			break OuterLoop
		default:
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}
		}
	}

	token = &ConfigToken{
		tokenType: CtkWord,
		value:     buffer.String(),
		endPos:    scanner.pos,
	}

	return
}

func (scanner *ConfigScanner) scanStringWord() (token *ConfigToken, err error) {
	var buffer bytes.Buffer
	var char rune
	var eof bool

	char, eof, err = scanner.read()
	if err != nil || eof {
		return
	}

	if _, err = buffer.WriteRune(char); err != nil {
		return
	}

	closingQuoteFound := false
	escape := false

OuterLoop:
	for {
		char, eof, err = scanner.read()

		switch {
		case err != nil:
			return
		case eof:
			break OuterLoop
		case char == '\\':
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}

			if !escape {
				escape = true
				continue
			}
		case char == '"':
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}

			if !escape {
				closingQuoteFound = true
				break OuterLoop
			}
		default:
			if _, err = buffer.WriteRune(char); err != nil {
				return
			}
		}

		escape = false
	}

	if closingQuoteFound {
		var word string
		word, err = scanner.processStringWord(buffer.String())
		if err != nil {
			return
		}

		token = &ConfigToken{
			tokenType: CtkWord,
			value:     word,
			endPos:    scanner.pos,
		}
	} else {
		token = &ConfigToken{
			tokenType: CtkInvalid,
			value:     buffer.String(),
			endPos:    scanner.pos,
			err:       errors.New("Unterminated string"),
		}
	}

	return
}

func (scanner *ConfigScanner) processStringWord(str string) (string, error) {
	var buffer bytes.Buffer
	chars := []rune(str)

	if len(chars) < 2 || chars[0] != '"' || chars[len(chars)-1] != '"' {
		return str, fmt.Errorf("Invalid string word: %v", str)
	}

	chars = chars[1 : len(chars)-1]
	escape := false

	for _, char := range chars {
		switch {
		case escape:
			switch char {
			case 'n':
				buffer.WriteRune('\n')
			case 't':
				buffer.WriteRune('\t')
			default:
				buffer.WriteRune(char)
			}

			escape = false
		case char == '\\':
			escape = true
		default:
			buffer.WriteRune(char)
		}
	}

	return buffer.String(), nil
}
