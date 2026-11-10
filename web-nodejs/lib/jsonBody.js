'use strict';

/**
 * JSON body parsing for the console. Bodies are capped at 2 MB (base64 logo
 * images are the largest regular ones).
 *
 * Ticket attachments, which may be larger, are read by the Go console's
 * attachment route (betterdesk-server/console/routes/tickets).
 */

const express = require('express');

const DEFAULT_LIMIT = '2mb';

function jsonBody() {
    return express.json({ limit: DEFAULT_LIMIT });
}

module.exports = { jsonBody, DEFAULT_LIMIT };
