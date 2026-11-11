package smtpwire

import (
	"strconv"
	"strings"
)

// envelopeState follows one message through MAIL FROM, RCPT TO and DATA.
type envelopeState struct {
	from string
	to   []string
	// unsent are the recipients RCPT TO was not sent for yet, awaiting the
	// ones whose reply is outstanding
	unsent   []string
	awaiting []string

	accepted       []string
	rejected       []string
	rejectedErrors []*Error
	// mailError is the refusal of the sender
	mailError *Error
	// pipelined says that all commands up to DATA went out at once
	pipelined bool
}

// newEnvelopeState checks the addresses of an envelope. Addresses are
// trimmed first. An address with a line break or an angle bracket could
// carry a second command or leave the brackets it is sent in, so it is
// refused before anything is written.
func newEnvelopeState(env *Envelope) (*envelopeState, *Error) {
	state := &envelopeState{
		from:     jsTrim(env.From),
		to:       make([]string, len(env.To)),
		accepted: []string{},
		rejected: []string{},
	}
	for i, to := range env.To {
		state.to[i] = jsTrim(to)
	}
	if len(state.to) == 0 {
		return nil, newError("No recipients defined", "EENVELOPE", "", "API")
	}
	if strings.ContainsAny(state.from, "\r\n<>") {
		return nil, newError("Invalid sender "+jsonQuote(state.from), "EENVELOPE", "", "API")
	}
	for _, to := range state.to {
		if to == "" || strings.ContainsAny(to, "\r\n<>") {
			return nil, newError("Invalid recipient "+jsonQuote(to), "EENVELOPE", "", "API")
		}
	}
	state.unsent = append([]string(nil), state.to...)
	return state, nil
}

// needsSMTPUTF8 reports whether an address has characters outside ASCII.
func (e *envelopeState) needsSMTPUTF8() bool {
	if hasNonASCII(e.from) {
		return true
	}
	for _, to := range e.to {
		if hasNonASCII(to) {
			return true
		}
	}
	return false
}

// mailCommand builds MAIL FROM with the parameters the server's extensions
// allow.
func mailCommand(state *envelopeState, env *Envelope, ehlo *ehloInfo) (command string, smtpUTF8 bool) {
	var args []string
	if state.needsSMTPUTF8() && ehlo.smtpUTF8 {
		args = append(args, "SMTPUTF8")
		smtpUTF8 = true
	}
	if env.Use8BitMime && ehlo.eightBitMIME {
		args = append(args, "BODY=8BITMIME")
	}
	if env.Size > 0 && ehlo.size {
		args = append(args, "SIZE="+strconv.FormatInt(env.Size, 10))
	}
	command = "MAIL FROM:<" + state.from + ">"
	if len(args) > 0 {
		command += " " + strings.Join(args, " ")
	}
	return command, smtpUTF8
}

// failure decides how the envelope went once every reply to MAIL FROM and
// RCPT TO is in. reply is the reply being handled. It returns nil when a
// recipient was accepted and the message can be sent.
func (e *envelopeState) failure(reply string) *Error {
	if e.mailError != nil {
		return e.mailError
	}
	if len(e.accepted) > 0 {
		return nil
	}
	// The error carries the reply being handled, with PIPELINING the last
	// refusal instead. A temporary refusal goes before both: the message
	// may still go through later.
	if e.pipelined && len(e.rejectedErrors) > 0 {
		reply = e.rejectedErrors[len(e.rejectedErrors)-1].Response
	}
	for _, rejected := range e.rejectedErrors {
		if rejected.ResponseCode != 0 && rejected.ResponseCode < 500 {
			reply = rejected.Response
			break
		}
	}
	err := newError("Can't send mail - all recipients were rejected", "EENVELOPE", reply, "RCPT TO")
	err.Rejected = e.rejected
	err.RejectedErrors = e.rejectedErrors
	return err
}

// newError builds a failure. A server reply is added to the message.
func newError(message, code, response, command string) *Error {
	err := &Error{Message: message, Code: code, Command: command}
	if response != "" {
		err.Response = response
		err.Message += ": " + response
		err.ResponseCode = replyCode(response)
	}
	return err
}
