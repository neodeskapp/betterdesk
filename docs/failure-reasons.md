# Failure reasons

Every failure a person can meet in the console (the remote viewer, joins and
quick support) has a reason code. The server and the client send the code;
the console turns it into what happened, why and what to do, in the
console language. No text is made from an English error message: the
message goes under Details.

Where the codes come from:

- The server: `GET /api/peers/{id}/reach` (and the console's
  `GET /api/devices/{id}/reach`) answers `online` or one of `device_offline`,
  `device_unknown`, `device_revoked`, `device_blocked`, `device_banned`,
  `device_removed`, `key_changed`. A refused join or quick support device
  answers `{"error":"device_refused","reason":...}` with `device_revoked`,
  `device_blocked`, `device_banned`, `device_removed`, `device_rejected`,
  `other_machine`, `key_changed` or `join_proof`, and a quick support
  session that ended for it carries the same code as `end_reason`.
- The signal server's PunchHoleResponse failure (`OFFLINE`, `ID_NOT_EXIST`,
  `LICENSE_MISMATCH`, `LICENSE_OVERUSE`) and its refusal values, listed with
  their codes in `web-ui/src/lib/remote/server-refusals.json` (a Go test in
  `neodesk-server/signal` fails when a refusal has no code).
- The computer's login refusals, listed in
  `web-ui/src/lib/remote/device-refusals.json`, and the ones whose text
  carries a number ("Too many wrong attempts for IPv6 prefix /64") in
  `web-ui/src/lib/remote/failures.ts`.
- The viewer's own checks: a connection that could not be secured, HTTPS,
  a close while signing in, a dropped network, and nobody accepting in time.

The texts are the `failure.<reason>.what`, `.why`, `.todo` keys of the
console's language files (`todo_ask` for someone who may not unblock).
`{name}` is the computer, `{person}` the person at it. A test
(`web-ui/tests/unit/remote/failures.test.ts`) checks that this table, the
code and the language files say the same.

## `device_offline`

| Part | English | German |
| --- | --- | --- |
| what | {name} is offline. | {name} ist offline. |
| why | It has not checked in with the server for a while. | Er hat sich schon eine Weile nicht beim Server gemeldet. |
| todo | Turn it on or wake it up, and check that NeoDesk runs on it. Then reconnect. | Schalte ihn ein oder wecke ihn auf und prüfe, ob NeoDesk darauf läuft. Verbinde dich dann neu. |

## `device_unknown`

| Part | English | German |
| --- | --- | --- |
| what | The server does not know {name}. | Der Server kennt {name} nicht. |
| why | No computer with this ID is registered on this server. | Auf diesem Server ist kein Computer mit dieser ID angemeldet. |
| todo | Check the ID. If the computer was removed, add it again under Add computers. | Prüfe die ID. Wenn der Computer entfernt wurde, füge ihn unter Computer hinzufügen wieder hinzu. |

## `device_revoked`

| Part | English | German |
| --- | --- | --- |
| what | {name} was revoked on this server. | {name} wurde auf diesem Server widerrufen. |
| why | Someone revoked this computer in the console, so the server refuses it, even when it is on. | Jemand hat diesen Computer in der Konsole widerrufen. Deshalb weist der Server ihn ab, auch wenn er an ist. |
| todo | If it should connect again, unblock it here. Then reconnect. | Wenn er sich wieder verbinden soll, entsperre ihn hier. Verbinde dich dann neu. |
| todo_ask | Ask an administrator to unblock this computer, then reconnect. | Bitte einen Administrator, diesen Computer zu entsperren, und verbinde dich dann neu. |

## `device_blocked`

| Part | English | German |
| --- | --- | --- |
| what | {name} is blocked on this server. | {name} ist auf diesem Server gesperrt. |
| why | Its ID is on the server's blocklist, so the server refuses it, even when it is on. | Seine ID steht auf der Sperrliste des Servers. Deshalb weist der Server ihn ab, auch wenn er an ist. |
| todo | If it should connect again, unblock it here. Then reconnect. | Wenn er sich wieder verbinden soll, entsperre ihn hier. Verbinde dich dann neu. |
| todo_ask | Ask an administrator to unblock this computer, then reconnect. | Bitte einen Administrator, diesen Computer zu entsperren, und verbinde dich dann neu. |

## `device_banned`

| Part | English | German |
| --- | --- | --- |
| what | {name} is banned on this server. | {name} ist auf diesem Server gebannt. |
| why | An administrator banned this computer, so the server refuses it. | Ein Administrator hat diesen Computer gebannt. Deshalb weist der Server ihn ab. |
| todo | If it should connect again, unban it in the device panel. | Wenn er sich wieder verbinden soll, hebe den Bann im Gerätebereich auf. |

## `device_removed`

| Part | English | German |
| --- | --- | --- |
| what | {name} was deleted on this server. | {name} wurde auf diesem Server gelöscht. |
| why | The computer is with the deleted devices. | Der Computer liegt bei den gelöschten Geräten. |
| todo | Restore it in the device panel, or add it again under Add computers. | Stelle ihn im Gerätebereich wieder her oder füge ihn unter Computer hinzufügen neu hinzu. |

## `device_rejected`

| Part | English | German |
| --- | --- | --- |
| what | This server turned {name} away. | Dieser Server hat {name} abgewiesen. |
| why | An administrator rejected this computer when it asked to join. | Ein Administrator hat diesen Computer abgelehnt, als er beitreten wollte. |
| todo | Ask an administrator to allow it under Add computers, then try again. | Bitte einen Administrator, ihn unter Computer hinzufügen zuzulassen, und versuche es dann noch einmal. |

## `other_machine`

| Part | English | German |
| --- | --- | --- |
| what | Another computer already uses the ID of {name}. | Ein anderer Computer nutzt die ID von {name} schon. |
| why | The server knows this ID from a different machine. | Der Server kennt diese ID von einem anderen Gerät. |
| todo | Ask an administrator to delete the old device in the console, then try again. | Bitte einen Administrator, das alte Gerät in der Konsole zu löschen, und versuche es dann noch einmal. |

## `key_changed`

| Part | English | German |
| --- | --- | --- |
| what | {name} came back with a new key. | {name} hat sich mit einem neuen Schlüssel zurückgemeldet. |
| why | The server refuses a computer whose key changed until an administrator approves it. This keeps others from taking over its ID. | Der Server weist einen Computer mit geändertem Schlüssel ab, bis ein Administrator ihn freigibt. So kann niemand seine ID übernehmen. |
| todo | If you expected the change, approve the new key in the device panel. Then reconnect. | Wenn du die Änderung erwartet hast, gib den neuen Schlüssel im Gerätebereich frei. Verbinde dich dann neu. |

## `join_proof`

| Part | English | German |
| --- | --- | --- |
| what | {name} could not prove that it is the computer the server knows. | {name} konnte nicht beweisen, dass er der Computer ist, den der Server kennt. |
| why | The ID is enrolled already, and this computer did not sign the join with the key the server has for it. | Die ID ist schon angemeldet, und dieser Computer hat den Beitritt nicht mit dem Schlüssel unterschrieben, den der Server für ihn hat. |
| todo | Update NeoDesk on this computer and open the link again, or ask an administrator to delete the old device in the console. | Aktualisiere NeoDesk auf diesem Computer und öffne den Link noch einmal, oder bitte einen Administrator, das alte Gerät in der Konsole zu löschen. |

## `server_unreachable`

| Part | English | German |
| --- | --- | --- |
| what | This browser can't reach the NeoDesk server. | Dieser Browser erreicht den NeoDesk-Server nicht. |
| why | The server did not answer when the connection started. | Der Server hat beim Verbindungsaufbau nicht geantwortet. |
| todo | Check your network, then reconnect. If it keeps failing, ask an administrator to check the server. | Prüfe dein Netzwerk und verbinde dich neu. Wenn es weiter scheitert, bitte einen Administrator, den Server zu prüfen. |

## `relay_unreachable`

| Part | English | German |
| --- | --- | --- |
| what | The connection to {name} could not be set up. | Die Verbindung zu {name} kam nicht zustande. |
| why | This browser could not reach the part of the server that carries the session. | Dieser Browser hat den Teil des Servers nicht erreicht, der die Sitzung überträgt. |
| todo | Reconnect. If it keeps failing, ask an administrator to check that the server's ports are open. | Verbinde dich neu. Wenn es weiter scheitert, bitte einen Administrator zu prüfen, ob die Ports des Servers offen sind. |

## `relay_refused`

| Part | English | German |
| --- | --- | --- |
| what | The server stopped the connection to {name}. | Der Server hat die Verbindung zu {name} gestoppt. |
| why | The server did not let this session through. | Der Server hat diese Sitzung nicht durchgelassen. |
| todo | Reconnect. If it keeps happening, copy the details and send them to an administrator. | Verbinde dich neu. Wenn es wieder passiert, kopiere die Details und schicke sie einem Administrator. |

## `no_answer`

| Part | English | German |
| --- | --- | --- |
| what | {name} did not answer. | {name} hat nicht geantwortet. |
| why | The server asked {name} to connect, and no answer came in time. It may be asleep or busy, or its network may block NeoDesk. | Der Server hat {name} um eine Verbindung gebeten, und es kam keine Antwort. Er schläft vielleicht, ist beschäftigt, oder sein Netzwerk blockiert NeoDesk. |
| todo | Wake it up or wait a moment, then reconnect. | Wecke ihn auf oder warte kurz und verbinde dich dann neu. |

## `not_authorized`

| Part | English | German |
| --- | --- | --- |
| what | You can't connect to {name}. | Du kannst dich nicht mit {name} verbinden. |
| why | The server does not let your account start sessions to this computer. | Der Server lässt dein Konto keine Sitzungen zu diesem Computer starten. |
| todo | Ask an administrator to give you access to this computer. | Bitte einen Administrator, dir Zugriff auf diesen Computer zu geben. |

## `feature_off`

| Part | English | German |
| --- | --- | --- |
| what | This kind of session is turned off for your account. | Diese Art von Sitzung ist für dein Konto ausgeschaltet. |
| why | An administrator turned it off in your account's client features. | Ein Administrator hat sie in den Client-Funktionen deines Kontos ausgeschaltet. |
| todo | Ask an administrator to turn it on. | Bitte einen Administrator, sie einzuschalten. |

## `consent_scope`

| Part | English | German |
| --- | --- | --- |
| what | {person} did not allow this. | {person} hat das nicht erlaubt. |
| why | In this quick support session, {person} allowed only some things. | In dieser Schnellhilfe-Sitzung hat {person} nur manches erlaubt. |
| todo | Ask {person} to allow it in the quick support window. | Bitte {person}, es im Schnellhilfe-Fenster zu erlauben. |

## `license_blocked`

| Part | English | German |
| --- | --- | --- |
| what | The server's license does not allow this connection. | Die Lizenz des Servers erlaubt diese Verbindung nicht. |
| why | The license key does not match this server. | Der Lizenzschlüssel passt nicht zu diesem Server. |
| todo | Ask an administrator to check the license. | Bitte einen Administrator, die Lizenz zu prüfen. |

## `session_limit`

| Part | English | German |
| --- | --- | --- |
| what | The server takes no more sessions right now. | Der Server nimmt gerade keine weiteren Sitzungen an. |
| why | The number of sessions is at the limit of its license. | Die Zahl der Sitzungen hat die Grenze der Lizenz erreicht. |
| todo | Wait until another session ends, or ask an administrator. | Warte, bis eine andere Sitzung endet, oder frag einen Administrator. |

## `billing_blocked`

| Part | English | German |
| --- | --- | --- |
| what | The server does not allow sessions to {name} right now. | Der Server erlaubt gerade keine Sitzungen zu {name}. |
| why | The contract of this computer does not allow sessions. | Der Vertrag dieses Computers erlaubt keine Sitzungen. |
| todo | Ask an administrator to check the computer's contract. | Bitte einen Administrator, den Vertrag des Computers zu prüfen. |

## `client_too_old`

| Part | English | German |
| --- | --- | --- |
| what | NeoDesk on {name} is too old for this. | NeoDesk auf {name} ist dafür zu alt. |
| why | The version on the computer does not support this connection. | Die Version auf dem Computer unterstützt diese Verbindung nicht. |
| todo | Update NeoDesk on {name}, then reconnect. | Aktualisiere NeoDesk auf {name} und verbinde dich dann neu. |

## `consent_declined`

| Part | English | German |
| --- | --- | --- |
| what | {person} declined the connection. | {person} hat die Verbindung abgelehnt. |
| why | The person at {name} clicked Decline or closed the window that asked. | Die Person an {name} hat auf Ablehnen geklickt oder das Fenster mit der Anfrage geschlossen. |
| todo | Ask them to try again, then reconnect. | Bitte sie, es noch einmal zu versuchen, und verbinde dich dann neu. |

## `consent_timeout`

| Part | English | German |
| --- | --- | --- |
| what | {person} did not answer. | {person} hat nicht geantwortet. |
| why | Nobody accepted the connection on {name} within 3 minutes. | Niemand hat die Verbindung an {name} innerhalb von 3 Minuten angenommen. |
| todo | Ask them to watch for the window that asks, then reconnect. | Bitte sie, auf das Fenster mit der Anfrage zu achten, und verbinde dich dann neu. |

## `password_wrong`

| Part | English | German |
| --- | --- | --- |
| what | The password was not accepted. | Das Passwort wurde nicht angenommen. |
| why | {name} refused the password. | {name} hat das Passwort abgewiesen. |
| todo | Enter the password set on {name}. NeoDesk shows it on that computer under Settings, Security. | Gib das Passwort ein, das auf {name} gesetzt ist. NeoDesk zeigt es auf diesem Computer unter Einstellungen, Sicherheit. |

## `tfa_wrong`

| Part | English | German |
| --- | --- | --- |
| what | The code was not accepted. | Der Code wurde nicht angenommen. |
| why | {name} refused the two-factor code. | {name} hat den Zwei-Faktor-Code abgewiesen. |
| todo | Enter the current code from the authenticator app linked to {name}. | Gib den aktuellen Code aus der Authenticator-App ein, die mit {name} verknüpft ist. |

## `rate_limited`

| Part | English | German |
| --- | --- | --- |
| what | Too many tries. | Zu viele Versuche. |
| why | {name} blocks sign-ins for a while after several wrong ones. | {name} sperrt die Anmeldung eine Weile nach mehreren falschen Versuchen. |
| todo | Wait a minute, then reconnect. | Warte eine Minute und verbinde dich dann neu. |

## `device_not_allowed`

| Part | English | German |
| --- | --- | --- |
| what | {name} does not take this connection. | {name} nimmt diese Verbindung nicht an. |
| why | NeoDesk on {name} only lets people in with a password, and no password is set there, or its settings turn connections off. | NeoDesk auf {name} lässt nur mit Passwort herein, und dort ist keins gesetzt, oder seine Einstellungen schalten Verbindungen aus. |
| todo | Ask someone at {name} to set a password or allow connections under Settings, Security. | Bitte jemanden an {name}, unter Einstellungen, Sicherheit ein Passwort zu setzen oder Verbindungen zu erlauben. |

## `device_blocks_you`

| Part | English | German |
| --- | --- | --- |
| what | {name} blocks you. | {name} sperrt dich. |
| why | NeoDesk on {name} has your ID or address on its own blocklist. | NeoDesk auf {name} hat deine ID oder Adresse auf seiner eigenen Sperrliste. |
| todo | Ask someone at {name} to remove the block in its settings. | Bitte jemanden an {name}, die Sperre in den Einstellungen zu entfernen. |

## `device_feature_off`

| Part | English | German |
| --- | --- | --- |
| what | {name} turned this off. | {name} hat das ausgeschaltet. |
| why | NeoDesk on {name} does not allow this kind of session. | NeoDesk auf {name} erlaubt diese Art von Sitzung nicht. |
| todo | Ask someone at {name} to allow it under Settings, Security. | Bitte jemanden an {name}, es unter Einstellungen, Sicherheit zu erlauben. |

## `device_not_installed`

| Part | English | German |
| --- | --- | --- |
| what | {name} can't do this without an installed NeoDesk. | {name} kann das ohne installiertes NeoDesk nicht. |
| why | NeoDesk runs there without being installed, and this needs the installed version. | NeoDesk läuft dort, ohne installiert zu sein, und dafür braucht es die installierte Version. |
| todo | Ask someone at {name} to install NeoDesk, then reconnect. | Bitte jemanden an {name}, NeoDesk zu installieren, und verbinde dich dann neu. |

## `login_screen`

| Part | English | German |
| --- | --- | --- |
| what | {name} shows a sign-in screen NeoDesk can't share. | {name} zeigt einen Anmeldebildschirm, den NeoDesk nicht teilen kann. |
| why | The sign-in screen of {name} runs on Wayland, which NeoDesk can't capture. | Der Anmeldebildschirm von {name} läuft unter Wayland, das NeoDesk nicht aufnehmen kann. |
| todo | Sign in on {name} first, or switch its sign-in screen to X11. Then reconnect. | Melde dich zuerst an {name} an oder stelle den Anmeldebildschirm auf X11 um. Verbinde dich dann neu. |

## `privacy_mode`

| Part | English | German |
| --- | --- | --- |
| what | Someone turned on privacy mode on {name}. | Jemand hat an {name} den Privatsphäremodus eingeschaltet. |
| why | Another session holds {name} in privacy mode, which lets no second viewer in. | Eine andere Sitzung hält {name} im Privatsphäremodus, der keinen zweiten Zuschauer hereinlässt. |
| todo | Wait until that session ends, then reconnect. | Warte, bis diese Sitzung endet, und verbinde dich dann neu. |

## `device_not_ready`

| Part | English | German |
| --- | --- | --- |
| what | {name} is not ready for this connection. | {name} ist für diese Verbindung nicht bereit. |
| why | NeoDesk on {name} said it can't take the session now. | NeoDesk auf {name} hat gemeldet, dass es die Sitzung jetzt nicht annehmen kann. |
| todo | Ask someone at {name} to open NeoDesk, then reconnect. | Bitte jemanden an {name}, NeoDesk zu öffnen, und verbinde dich dann neu. |

## `insecure`

| Part | English | German |
| --- | --- | --- |
| what | The connection to {name} was stopped because it could not be secured. | Die Verbindung zu {name} wurde gestoppt, weil sie nicht gesichert werden konnte. |
| why | {name} did not prove its identity or did not encrypt the connection. Someone between this browser and the computer may be listening in, or the server key is not set up. | {name} hat seine Identität nicht bewiesen oder die Verbindung nicht verschlüsselt. Vielleicht hört jemand zwischen diesem Browser und dem Computer mit, oder der Serverschlüssel ist nicht eingerichtet. |
| todo | No password was sent. Ask an administrator to check the server. | Es wurde kein Passwort gesendet. Bitte einen Administrator, den Server zu prüfen. |

## `https_required`

| Part | English | German |
| --- | --- | --- |
| what | {name} can't be shown over plain HTTP. | {name} kann über einfaches HTTP nicht angezeigt werden. |
| why | This computer sends a video format the browser plays only over HTTPS. | Dieser Computer sendet ein Videoformat, das der Browser nur über HTTPS abspielt. |
| todo | Open the console over HTTPS to see its screen. | Öffne die Konsole über HTTPS, um den Bildschirm zu sehen. |

## `signin_closed`

| Part | English | German |
| --- | --- | --- |
| what | {name} closed the connection when this browser signed in. | {name} hat die Verbindung geschlossen, als sich dieser Browser angemeldet hat. |
| why | The computer refused the sign-in. | Der Computer hat die Anmeldung abgewiesen. |
| todo | Reconnect and sign in again. If it keeps happening, ask an administrator to look at the log of NeoDesk on the computer. | Verbinde dich neu und melde dich noch einmal an. Wenn es wieder passiert, bitte einen Administrator, sich das Protokoll von NeoDesk auf dem Computer anzusehen. |

## `dropped_early`

| Part | English | German |
| --- | --- | --- |
| what | The connection closed before {name} answered. | Die Verbindung wurde geschlossen, bevor {name} geantwortet hat. |
| why | The network between this browser, the server and {name} was interrupted while the session started. | Das Netzwerk zwischen diesem Browser, dem Server und {name} wurde beim Start der Sitzung unterbrochen. |
| todo | Reconnect. If it keeps happening, check the network of the computer. | Verbinde dich neu. Wenn es wieder passiert, prüfe das Netzwerk des Computers. |

## `dropped`

| Part | English | German |
| --- | --- | --- |
| what | The connection to {name} dropped. | Die Verbindung zu {name} ist abgebrochen. |
| why | The network between this browser, the server and {name} was interrupted. | Das Netzwerk zwischen diesem Browser, dem Server und {name} wurde unterbrochen. |
| todo | Reconnect. If it keeps happening, check the network of the computer. | Verbinde dich neu. Wenn es wieder passiert, prüfe das Netzwerk des Computers. |

## `unknown`

| Part | English | German |
| --- | --- | --- |
| what | Could not connect to {name}. | Die Verbindung zu {name} ist fehlgeschlagen. |
| why | The cause is not known. The details below show what this browser saw. | Die Ursache ist nicht bekannt. Die Details unten zeigen, was dieser Browser gesehen hat. |
| todo | Reconnect. If it keeps failing, copy the details and send them to an administrator. | Verbinde dich neu. Wenn es weiter scheitert, kopiere die Details und schicke sie einem Administrator. |
