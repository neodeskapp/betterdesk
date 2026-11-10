'use strict';

// Runs the steps below through the Node console's CDAP studio routes on a
// new SQLite database and writes what they answered to ../flows.json, and
// the answer of the templates route to ../../templates.json. See README.md.

const fs = require('fs');
const os = require('os');
const path = require('path');
const http = require('http');

const consoleDir = process.argv[2] || '/repo/web-nodejs';
const tmpDir = fs.mkdtempSync(path.join(os.tmpdir(), 'bd-studio-oracle-'));
process.env.DATA_DIR = tmpDir;
process.env.SQLITE_AUTH_DB_MODE = 'consolidated';
process.env.DB_TYPE = 'sqlite';
process.env.DB_PATH = path.join(tmpDir, 'db_v2.sqlite3');
delete process.env.DATABASE_URL;

const express = require(path.join(consoleDir, 'node_modules/express'));

const users = {
    alice: { id: 2, username: 'alice', role: 'operator' },
    bob: { id: 3, username: 'bob', role: 'operator' },
    vera: { id: 4, username: 'vera', role: 'viewer' },
    root: { id: 1, username: 'root', role: 'super_admin' },
    adm: { id: 5, username: 'adm', role: 'admin' },
    gabi: { id: 6, username: 'gabi', role: 'global_admin' },
    paul: { id: 7, username: 'paul', role: 'pro' },
};

const flowJson = JSON.stringify({
    nodes: [{ id: 'n1', type: 'modbus-tcp', label: 'Read' }, { id: 'n2', type: 'log' }],
    wires: [{ from: 'n1', to: 'n2', fromPort: 'out', toPort: 'in' }],
});

const deep = (n) => '['.repeat(n) + ']'.repeat(n);

// A step: who sends what. body is sent as JSON; raw as it is, with type.
// save keeps the id of the answer under a name, and {name} in a later
// path stands for it.
const steps = [
    { name: 'signed out lists', as: null, method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'signed out creates', as: null, method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'x' } },
    { name: 'empty list', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'templates', as: 'vera', method: 'GET', path: '/api/cdap-studio/templates' },
    { name: 'templates signed out', as: null, method: 'GET', path: '/api/cdap-studio/templates' },

    { name: 'create', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: '  Temperature  ', description: '  Reads the PLC  ', flow_json: flowJson }, save: 'a1' },
    { name: 'list own', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'get', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'get in capitals', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{A1}' },
    { name: 'get unknown', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/nothing' },
    { name: 'viewer creates', as: 'vera', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'No', flow_json: '{}' } },
    { name: 'global admin creates', as: 'gabi', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'No', flow_json: '{}' } },

    // names
    { name: 'no name', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { flow_json: '{}' } },
    { name: 'blank name', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: ' \t ﻿ ', flow_json: '{}' } },
    { name: 'name is a number', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 5 } },
    { name: 'name of 101 characters', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'x'.repeat(101) } },
    { name: 'name of 100 characters', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: ' ' + 'x'.repeat(100) + ' ' }, save: 'n100' },
    { name: 'name of 51 characters outside the basic plane', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: '\u{1F600}'.repeat(51) } },
    { name: 'name of 50 characters outside the basic plane', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: '\u{1F600}'.repeat(50) }, save: 'n50' },
    { name: 'a list body', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', raw: '[{"name":"x"}]', type: 'application/json' },
    { name: 'description is cut', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Long', description: '  ' + 'd'.repeat(499) + '\u{1F600}tail' }, save: 'd500' },
    { name: 'get description is cut', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{d500}' },
    { name: 'description is no text', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'NoText', description: { a: 1 } }, save: 'dobj' },
    { name: 'get description is no text', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{dobj}' },

    // flow_json
    { name: 'flow as a value', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', raw: '{"name":"Value","flow_json":{ "b" : 1.50, "a":[1e3, 1E-7, -0, 0.000001, 1e21, 123456789012345678901234567890, 1e999, true, null, "t\\u00e9xt \\ud83d\\ude00 \\ud83d \\u2028 \\u0007 \\/ <>&", {"10":1,"2":2,"x":3,"1":4,"x":5}], "nodes":[] }}', type: 'application/json', save: 'val' },
    { name: 'get flow as a value', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{val}' },
    { name: 'flow as text is kept', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Text', flow_json: ' { "b" : 1.50,\n "a" : [ 1e3 ] } ' }, save: 'txt' },
    { name: 'get flow as text', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{txt}' },
    { name: 'flow left out', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Empty' }, save: 'none' },
    { name: 'get flow left out', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{none}' },
    { name: 'flow null', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Null', flow_json: null }, save: 'null' },
    { name: 'flow false', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'False', flow_json: false }, save: 'false' },
    { name: 'flow zero', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Zero', flow_json: 0 }, save: 'zero' },
    { name: 'flow five', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Five', flow_json: 5 }, save: 'five' },
    { name: 'flow true', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'True', flow_json: true }, save: 'true' },
    { name: 'flow list', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'List', flow_json: [] }, save: 'list' },
    { name: 'flow empty text', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '' } },
    { name: 'flow text that is not JSON', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '{nope' } },
    { name: 'flow text with a comment', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '{} // x' } },
    { name: 'flow text with a trailing comma', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '[1,]' } },
    { name: 'flow text with single quotes', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: "{'a':1}" } },
    { name: 'flow text with a leading zero', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '[01]' } },
    { name: 'flow text with a byte order mark', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '﻿{}' } },
    { name: 'flow text with a raw tab in a string', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '["a\tb"]' } },
    { name: 'flow text with a bad escape', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '["\\x41"]' } },
    { name: 'flow text with a short unicode escape', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '["\\u12"]' } },
    { name: 'flow text with a signed unicode escape', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '["\\u+123"]' } },
    { name: 'flow text NaN', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: 'NaN' } },
    { name: 'flow text number forms', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Numbers', flow_json: '[-0, 0.5, 1E+2, 1e-2, 12.0e0]' }, save: 'nums' },
    { name: 'flow text bad number forms', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '[.5]' } },
    { name: 'flow text a number with a dot at the end', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '[1.]' } },
    { name: 'flow text plus sign', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '[+1]' } },
    { name: 'flow text two values', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bad', flow_json: '{} {}' } },
    { name: 'flow text a plain string', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'String', flow_json: ' "just text" ' }, save: 'str' },
    { name: 'flow text nested 1000 deep', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Deep', flow_json: deep(1000) }, save: 'deep' },
    { name: 'export nested 1000 deep', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{deep}/export', hash: true },
    { name: 'delete nested', as: 'alice', method: 'DELETE', path: '/api/cdap-studio/flows/{deep}' },

    // update
    { name: 'update all', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: ' Temperature v2 ', description: ' Changed ', flow_json: { nodes: [{ id: 'n1', type: 'log' }], wires: [] } } },
    { name: 'get after update', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'update nothing', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: {} },
    { name: 'get after update of nothing', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'update with odd values', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: '   ', description: null, flow_json: null } },
    { name: 'get after update with odd values', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'update with a long name', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: 'y'.repeat(99) + '\u{1F600}z', description: 7 } },
    { name: 'get after update with a long name', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'update with a name that is no text', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: ['x'], description: 'kept name' } },
    { name: 'update with bad flow', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: 'not stored', flow_json: '{nope' } },
    { name: 'update with empty flow text', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { flow_json: '' } },
    { name: 'get after refused updates', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'update unknown', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/nothing', body: { flow_json: '{nope' } },
    { name: 'update by another operator', as: 'bob', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { flow_json: '{nope' } },
    { name: 'update by a viewer', as: 'vera', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: 'Mine' } },
    { name: 'update by a super admin', as: 'root', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { name: 'Checked', flow_json: flowJson } },
    { name: 'update by an admin', as: 'adm', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { description: 'by adm' } },
    { name: 'get after admins', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },

    // who sees what
    { name: 'create by bob', as: 'bob', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Bobs', flow_json: flowJson }, save: 'b1' },
    { name: 'create by root', as: 'root', method: 'POST', path: '/api/cdap-studio/flows', body: { name: 'Roots' }, save: 'r1' },
    { name: 'list by bob', as: 'bob', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'list by vera', as: 'vera', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'list by root', as: 'root', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'list by adm', as: 'adm', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'list by gabi', as: 'gabi', method: 'GET', path: '/api/cdap-studio/flows' },
    { name: 'get by another operator', as: 'bob', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'get by a viewer', as: 'vera', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'get by a super admin', as: 'root', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'export by another operator', as: 'bob', method: 'GET', path: '/api/cdap-studio/flows/{a1}/export' },
    { name: 'test by another operator', as: 'bob', method: 'POST', path: '/api/cdap-studio/flows/{a1}/test', body: {} },
    { name: 'test by a viewer', as: 'vera', method: 'POST', path: '/api/cdap-studio/flows/{a1}/test', body: {} },
    { name: 'deploy by another operator', as: 'bob', method: 'POST', path: '/api/cdap-studio/flows/{a1}/deploy', body: {} },
    { name: 'deploy by a viewer', as: 'vera', method: 'POST', path: '/api/cdap-studio/flows/{a1}/deploy', body: {} },
    { name: 'delete by another operator', as: 'bob', method: 'DELETE', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'delete by a viewer', as: 'vera', method: 'DELETE', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'delete signed out', as: null, method: 'DELETE', path: '/api/cdap-studio/flows/{a1}' },

    // deploy, test, export
    { name: 'deploy', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{a1}/deploy', body: {} },
    { name: 'get after deploy', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'deploy unknown', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/nothing/deploy', body: {} },
    { name: 'update keeps deployed', as: 'alice', method: 'PUT', path: '/api/cdap-studio/flows/{a1}', body: { description: 'still deployed' } },
    { name: 'get after update of deployed', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'test', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{a1}/test', body: {} },
    { name: 'test by a super admin', as: 'root', method: 'POST', path: '/api/cdap-studio/flows/{a1}/test', body: {} },
    { name: 'test unknown', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/nothing/test', body: {} },
    { name: 'export', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{a1}/export' },
    { name: 'export by a super admin', as: 'root', method: 'GET', path: '/api/cdap-studio/flows/{a1}/export' },
    { name: 'export unknown', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/nothing/export' },
    { name: 'export signed out', as: null, method: 'GET', path: '/api/cdap-studio/flows/{a1}/export' },
    { name: 'export flow as a value', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{val}/export' },
    { name: 'export name outside the basic plane', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{n50}/export' },
    { name: 'export empty flow', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{none}/export' },
    { name: 'export list flow', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{list}/export' },
    { name: 'export string flow', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{str}/export' },
    { name: 'export number forms', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{nums}/export' },
    { name: 'test flow as a value', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{val}/test', body: {} },
    { name: 'test empty flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{none}/test', body: {} },
    { name: 'test number flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{five}/test', body: {} },
    { name: 'test true flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{true}/test', body: {} },
    { name: 'test list flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{list}/test', body: {} },
    { name: 'test string flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/{str}/test', body: {} },
];

// Flows whose nodes and wires are not what the editor writes, each
// created, test run and exported.
const odd = {
    'nodes of every kind': { nodes: [{ id: 1, type: 2, label: 3 }, { id: null, type: null, label: null }, { id: 'a', type: 'b', label: '' }, { id: 'a', type: 'b', label: 0 }, { id: 'a', type: 'b', label: [] }, { id: { x: 1 }, type: ['t', 1, null, [2, [3]], {}], label: {} }, {}, 5, 'text', true, false, [], [1, 2], { type: true }, { type: 1e21 }, { type: -0 }, { type: 0.1 }, { type: 1e-7 }, { type: 1e999 }, { type: -1e999 }, { extra: 1, id: 'last' }], wires: [1, 2, 3] },
    'a null node': { nodes: [{ id: 'n1' }, null] },
    'a node type with toString': { nodes: [{ id: 'n1', type: { toString: 'x' } }] },
    'a node type list with toString inside': { nodes: [{ id: 'n1', type: [1, { toString: null }] }] },
    'a node type with valueOf': { nodes: [{ id: 'n1', type: { valueOf: 'x' } }] },
    'nodes as text': { nodes: 'ab\u{1F600}c', wires: 'w\u{1F600}' },
    'nodes as empty text': { nodes: '', wires: '' },
    'nodes as a number': { nodes: 5 },
    'nodes as zero': { nodes: 0, wires: 0 },
    'nodes as true': { nodes: true },
    'nodes as false': { nodes: false, wires: false },
    'nodes as an object': { nodes: { length: 2, 0: { id: 'a' } } },
    'nodes null': { nodes: null, wires: null },
    'wires as a number': { nodes: [], wires: 7 },
    'wires as true': { nodes: [], wires: true },
    'wires as an object with length': { nodes: [], wires: { length: 3 } },
    'wires as an object with an odd length': { nodes: [], wires: { length: { a: [1, 'x'] } } },
    'wires as an object without length': { nodes: [], wires: { a: 1 } },
    'wires with a null length': { nodes: [], wires: { length: null } },
    'no nodes key': { wires: [{}] },
    'keys in every order': { 2: 'b', b: 1, 1: 'a', a: 2, nodes: [{ label: 'l', type: 't', id: 'i' }] },
    'text that needs escapes': { nodes: [{ id: 'q"\\\b\f\n\r\t\u0001\u001f\u007f  /<>&', type: 'é€\u{1F600}', label: '\ud83d' }] },
};
for (const [name, flow] of Object.entries(odd)) {
    const key = 'odd' + steps.length;
    steps.push({ name: `odd flow: ${name}`, as: 'bob', method: 'POST', path: '/api/cdap-studio/flows', body: { name: `Odd ${name}`, flow_json: flow }, save: key });
    steps.push({ name: `test: ${name}`, as: 'bob', method: 'POST', path: `/api/cdap-studio/flows/{${key}}/test`, body: {} });
    steps.push({ name: `export: ${name}`, as: 'bob', method: 'GET', path: `/api/cdap-studio/flows/{${key}}/export` });
    steps.push({ name: `get: ${name}`, as: 'bob', method: 'GET', path: `/api/cdap-studio/flows/{${key}}` });
    steps.push({ name: `delete: ${name}`, as: 'bob', method: 'DELETE', path: `/api/cdap-studio/flows/{${key}}` });
}

steps.push(
    // import
    { name: 'import', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { version: '1.0', format: 'bdflow', name: '  Copy  ', description: '  of a flow  ', flow: JSON.parse(flowJson) }, save: 'i1' },
    { name: 'get imported', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{i1}' },
    { name: 'import without a name', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { flow: [] }, save: 'i2' },
    { name: 'get imported without a name', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{i2}' },
    { name: 'import with a long name', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'z'.repeat(150), description: 9, flow: {} }, save: 'i3' },
    { name: 'get imported with a long name', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{i3}' },
    { name: 'import with a name that is no text', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 7, flow: { a: 1 } }, save: 'i4' },
    { name: 'import without a flow', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'Bad' } },
    { name: 'import flow null', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'Bad', flow: null } },
    { name: 'import flow as text', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'Bad', flow: '{}' } },
    { name: 'import flow as a number', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'Bad', flow: 5 } },
    { name: 'import flow true', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', body: { name: 'Bad', flow: true } },
    { name: 'import as a form', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', raw: 'name=Form&flow=%7B%7D', type: 'application/x-www-form-urlencoded' },
    { name: 'import as a form with two flows', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import', raw: 'name=Form&description=d&flow=a&flow=b%20c', type: 'application/x-www-form-urlencoded', save: 'i5' },
    { name: 'get imported form', as: 'alice', method: 'GET', path: '/api/cdap-studio/flows/{i5}' },
    { name: 'import without a body', as: 'alice', method: 'POST', path: '/api/cdap-studio/flows/import' },
    { name: 'import by a viewer', as: 'vera', method: 'POST', path: '/api/cdap-studio/flows/import', body: { flow: {} } },
    { name: 'import signed out', as: null, method: 'POST', path: '/api/cdap-studio/flows/import', body: { flow: {} } },

    // delete
    { name: 'delete unknown', as: 'alice', method: 'DELETE', path: '/api/cdap-studio/flows/nothing' },
    { name: 'delete by a super admin', as: 'root', method: 'DELETE', path: '/api/cdap-studio/flows/{b1}' },
    { name: 'get deleted', as: 'bob', method: 'GET', path: '/api/cdap-studio/flows/{b1}' },
    { name: 'delete', as: 'alice', method: 'DELETE', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'delete again', as: 'alice', method: 'DELETE', path: '/api/cdap-studio/flows/{a1}' },
    { name: 'list at the end', as: 'root', method: 'GET', path: '/api/cdap-studio/flows' },
);

function send(port, step, saved) {
    const target = step.path.replace(/\{(\w+)\}/g, (_, key) => (key === 'A1' ? saved.a1.toUpperCase() : saved[key] || 'unsaved'));
    const headers = {};
    let payload = null;
    if (step.body !== undefined) {
        payload = Buffer.from(JSON.stringify(step.body));
        headers['Content-Type'] = 'application/json';
    } else if (step.raw !== undefined) {
        payload = Buffer.from(step.raw);
        headers['Content-Type'] = step.type;
    }
    if (payload) headers['Content-Length'] = payload.length;
    if (step.as) headers['x-user'] = step.as;
    return new Promise((resolve, reject) => {
        const req = http.request({ host: '127.0.0.1', port, method: step.method, path: target, headers }, (res) => {
            const chunks = [];
            res.on('data', (chunk) => chunks.push(chunk));
            res.on('end', () => resolve({ status: res.statusCode, headers: res.headers, text: Buffer.concat(chunks).toString('utf8') }));
        });
        req.on('error', reject);
        if (payload) req.write(payload);
        req.end();
    });
}

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

async function main() {
    const adapter = require(path.join(consoleDir, 'services/dbAdapter')).getAdapter();
    await adapter.init();
    const routes = require(path.join(consoleDir, 'routes/cdap-studio.routes'));

    // The body parsers of server.js, and a session per request
    const app = express();
    app.use(express.json({ limit: '2mb' }));
    app.use(express.urlencoded({ extended: false, limit: '2mb' }));
    app.use((req, _res, next) => {
        const user = users[req.headers['x-user']];
        req.session = user ? { userId: user.id, user } : {};
        req.t = (key) => key;
        next();
    });
    app.use(routes);
    const server = app.listen(0, '127.0.0.1');
    await new Promise((resolve) => server.once('listening', resolve));
    const { port } = server.address();

    const saved = {};
    const out = [];
    const quiet = console.error;
    console.error = () => {};
    for (const step of steps) {
        const answer = await send(port, step, saved);
        if (step.save) {
            saved[step.save] = JSON.parse(answer.text).id;
            if (typeof saved[step.save] !== 'string') throw new Error(`${step.name}: no id in ${answer.text}`);
        }
        const record = {
            name: step.name, as: step.as, method: step.method, path: step.path, save: step.save, id: step.save ? saved[step.save] : undefined,
            status: answer.status,
            type: answer.headers['content-type'] || '',
            disposition: answer.headers['content-disposition'] || '',
        };
        if (step.body !== undefined) record.body = JSON.stringify(step.body);
        if (step.raw !== undefined) { record.body = step.raw; record.bodyType = step.type; }
        if (step.hash) record.length = answer.text.length; else record.text = answer.text;
        out.push(record);
        if (step.name === 'templates') fs.writeFileSync(path.join(__dirname, '..', '..', 'templates.json'), answer.text);
        // Two writes in one millisecond would have the same time
        if (step.method !== 'GET') await sleep(3);
    }
    console.error = quiet;
    server.close();
    await adapter.close();
    fs.rmSync(tmpDir, { recursive: true, force: true });

    const file = path.join(__dirname, '..', 'flows.json');
    fs.writeFileSync(file, out.map((record) => JSON.stringify(record)).join('\n') + '\n');
    console.log(`${out.length} steps written to ${file}`);
}

main().catch((err) => { console.error(err); process.exit(1); });
