package ws

// Query is the search of the request target without its "?", as
// new URL(req.url).search held it: percent-encoded as the URL Standard
// leaves it, the fragment cut off. "" when there is none or it is empty.
func (u *Upgrade) Query() string { return u.query }

// HandshakeValid reports whether Accept would answer the handshake with
// 101: the websocket library's checks pass (see checkHandshake).
func (u *Upgrade) HandshakeValid() bool {
	_, refused := checkHandshake(u.R)
	return refused == ""
}

// Drop ends the connection without writing anything. It stands where a
// request made the old console's process end (DECISIONS A19): the client
// saw its connection end without an answer, and so it does here, but the
// console goes on.
func (u *Upgrade) Drop() {
	u.refuse("")
}
