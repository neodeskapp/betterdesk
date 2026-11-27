/**
 * Devices › Add › Receiver app (console/routes/receiverapp): the values of
 * a silent install of the NeoDesk app for this server, and the download
 * of the release's MSI with an install.cmd. Both need enrollment.manage.
 */

import * as v from 'valibot';
import { ApiError, api, clientMessages, getCsrfToken } from './client.ts';

const Str = v.optional(
	v.pipe(
		v.nullable(v.string()),
		v.transform((s) => s ?? '')
	),
	''
);

export const ReceiverServer = v.object({ id_server: Str, key: Str, api_server: Str });
export type ReceiverServer = v.InferOutput<typeof ReceiverServer>;

export const ReceiverPackage = v.object({ arch: v.string(), name: v.string(), size: v.number() });
export type ReceiverPackage = v.InferOutput<typeof ReceiverPackage>;

export const ReceiverSetup = v.object({
	server: ReceiverServer,
	release: v.nullable(v.object({ tag: v.string(), packages: v.array(ReceiverPackage) }))
});
export type ReceiverSetup = v.InferOutput<typeof ReceiverSetup>;

export function getReceiverSetup(signal?: AbortSignal): Promise<ReceiverSetup> {
	return api(ReceiverSetup, '/api/panel/receiver/setup', { signal });
}

export interface InstallerInput {
	token: string;
	/** A folder id; 0 for none */
	group: number;
	arch: string;
}

/**
 * The zip with neodesk-support.msi and install.cmd. The token is in the
 * body, not the address, so it stays out of logs. Errors come as JSON
 * with the server's sentence.
 */
export async function downloadInstaller(body: InstallerInput): Promise<{ blob: Blob; filename: string }> {
	let res: Response;
	try {
		res = await fetch('/api/panel/receiver/installer', {
			method: 'POST',
			credentials: 'same-origin',
			headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': getCsrfToken() },
			body: JSON.stringify({ token: body.token, group: body.group || null, arch: body.arch })
		});
	} catch {
		throw new ApiError(clientMessages.network, 0);
	}
	if (!res.ok) {
		let data: unknown = null;
		try {
			data = await res.json();
		} catch {
			/* not JSON */
		}
		const o = (data && typeof data === 'object' ? data : {}) as { error?: unknown; code?: unknown };
		const message =
			res.status === 403
				? clientMessages.permission
				: typeof o.error === 'string' && o.error
					? o.error
					: clientMessages.failed.replace('{status}', String(res.status));
		throw new ApiError(message, res.status, { code: typeof o.code === 'string' ? o.code : undefined, data });
	}
	const blob = await res.blob();
	const match = (res.headers.get('Content-Disposition') ?? '').match(/filename="?([^";]+)"?/i);
	return { blob, filename: match?.[1] || 'neodesk-support.zip' };
}
