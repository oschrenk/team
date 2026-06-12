package client

import (
	"fmt"

	"github.com/oschrenk/team/internal/paths"
	"github.com/oschrenk/team/internal/protocol"
	"github.com/oschrenk/team/internal/validate"
)

// FormatMsg renders a delivered protocol.Msg for stdout. The body is
// sanitized and truncated. Returns (line, contLine, wasTruncated): if
// wasTruncated, callers should print contLine on its own line right
// after.
//
// Format matches the Python client.py::_format_msg:
//
//	[team msg=<id> from="<name>" "<label>"] <text>
//	[team msg=<id> from="<name>" "<label>" truncated=<full>] <truncated>
//	[team msg=<id> cont] full text <full> bytes at <messages.log>
func FormatMsg(m protocol.Msg) (line, contLine string, wasTruncated bool) {
	sanitized := validate.SanitizeForStdout(m.Text)
	truncated, was, full := validate.TruncateForStdout(sanitized, protocol.StdoutCap)

	fromName := m.FromName
	if fromName == "" {
		// Fallback to a short prefix of the sender's session id.
		if len(m.From) > 8 {
			fromName = m.From[:8]
		} else {
			fromName = m.From
		}
	}
	labelPart := ""
	if m.FromLabel != "" {
		labelPart = fmt.Sprintf(` "%s"`, m.FromLabel)
	}
	var prefix string
	if was {
		prefix = fmt.Sprintf(`[team msg=%s from="%s"%s truncated=%d]`,
			m.MsgID, fromName, labelPart, full)
	} else {
		prefix = fmt.Sprintf(`[team msg=%s from="%s"%s]`,
			m.MsgID, fromName, labelPart)
	}
	line = fmt.Sprintf("%s %s", prefix, truncated)
	if was {
		contLine = fmt.Sprintf("[team msg=%s cont] full text %d bytes at %s",
			m.MsgID, full, paths.MessagesLogPath())
	}
	return line, contLine, was
}
