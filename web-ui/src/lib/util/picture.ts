/**
 * Profile pictures before upload: a photo of any size becomes a square of
 * at most 256 px (cut from the middle), as PNG, or as JPEG when the PNG
 * would still be over the server's 512 KB. A GIF that fits stays as it is,
 * so it keeps moving. When the browser can't read the file as a picture,
 * the file goes as it is and the server says what is wrong with it.
 */

export const PICTURE_SIZE = 256;

function blobOf(canvas: HTMLCanvasElement, type: string, quality?: number): Promise<Blob | null> {
	return new Promise((resolve) => canvas.toBlob(resolve, type, quality));
}

export async function shrinkPicture(file: File, maxBytes: number): Promise<Blob> {
	if (file.type === 'image/gif' && file.size <= maxBytes) return file;
	if (typeof createImageBitmap !== 'function' || typeof document === 'undefined') return file;
	let bitmap: ImageBitmap;
	try {
		bitmap = await createImageBitmap(file);
	} catch {
		return file;
	}
	try {
		const side = Math.min(bitmap.width, bitmap.height);
		if (!side) return file;
		const size = Math.min(PICTURE_SIZE, side);
		const canvas = document.createElement('canvas');
		canvas.width = canvas.height = size;
		const ctx = canvas.getContext('2d');
		if (!ctx) return file;
		ctx.imageSmoothingQuality = 'high';
		const sx = (bitmap.width - side) / 2;
		const sy = (bitmap.height - side) / 2;
		ctx.drawImage(bitmap, sx, sy, side, side, 0, 0, size, size);
		const png = await blobOf(canvas, 'image/png');
		if (png && png.size <= maxBytes) return png;
		const jpeg = await blobOf(canvas, 'image/jpeg', 0.9);
		return jpeg ?? file;
	} finally {
		bitmap.close();
	}
}
