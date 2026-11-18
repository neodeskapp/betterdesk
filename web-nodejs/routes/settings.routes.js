/**
 * BetterDesk Console - Settings Routes
 */

const express = require('express');
const router = express.Router();
const path = require('path');
const fs = require('fs');
const crypto = require('crypto');
const config = require('../config/config');
const db = require('../services/database');
const brandingService = require('../services/brandingService');
const fontService = require('../services/fontService');
const { resolveChildPath } = require('../lib/safePath');
const { unlinkMulterFiles } = require('../lib/multerCleanup');
const { requireAuth, requirePermission } = require('../middleware/auth');
const multer = require('multer');

// ==================== Branding / Theming API ====================

/**
 * POST /api/settings/branding - Save branding configuration (admin only)
 */
router.post('/api/settings/branding', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const updates = req.body;
        if (!updates || typeof updates !== 'object') {
            return res.status(400).json({ success: false, error: 'Invalid branding data' });
        }
        
        await brandingService.saveBranding(updates);
        const savedBranding = brandingService.getBranding();
        const readability = brandingService.assessAppearanceReadability(savedBranding);

        const keys = Object.keys(updates || {});
        const themeOnly = keys.length > 0 && keys.every((k) =>
            k === 'themeMode' || k === 'colors' || k === 'glassColor'
        ) && updates.themeMode;
        const auditAction = themeOnly ? 'theme_toggle' : 'branding_update';
        const auditDetails = themeOnly
            ? `Theme switched to ${updates.themeMode}`
            : 'Updated branding configuration';
        await db.logAction(req.session?.userId, auditAction, auditDetails, req.ip);
        
        res.json({
            success: true,
            data: savedBranding,
            appearance: brandingService.getAppearanceModel(savedBranding),
            readability,
            revision: brandingService.getBrandingRevision()
        });
    } catch (err) {
        console.error('Save branding error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

// ── Logo image upload (disk storage) ─────────────────────────────────────────
const UPLOADS_DIR = path.join(config.dataDir || path.join(__dirname, '..', 'data'), 'uploads');
fs.mkdirSync(UPLOADS_DIR, { recursive: true });

const logoUpload = multer({
    storage: multer.diskStorage({
        destination: (_req, _file, cb) => cb(null, UPLOADS_DIR),
        filename: (_req, file, cb) => {
            const ext = path.extname(file.originalname).toLowerCase() || '.png';
            const hash = crypto.randomBytes(8).toString('hex');
            cb(null, `logo-${hash}${ext}`);
        }
    }),
    limits: { fileSize: 2 * 1024 * 1024 },
    fileFilter: (_req, file, cb) => {
        const allowed = /^image\/(png|jpeg|gif|webp|svg\+xml)$/;
        if (allowed.test(file.mimetype)) {
            cb(null, true);
        } else {
            cb(new Error('Invalid file type'));
        }
    }
});

/**
 * POST /api/settings/branding/upload-logo - Upload logo image to server disk
 */
router.post('/api/settings/branding/upload-logo', requireAuth, requirePermission('branding.edit'), (req, res) => {
    logoUpload.single('logo')(req, res, async (err) => {
        if (err) {
            unlinkMulterFiles(req);
            const msg = err.code === 'LIMIT_FILE_SIZE' ? 'File too large (max 2 MB)' : (err.message || 'Upload failed');
            return res.status(400).json({ success: false, error: msg });
        }
        if (!req.file) {
            return res.status(400).json({ success: false, error: 'No file provided' });
        }
        const url = `/uploads/${req.file.filename}`;

        // Remove previous uploaded logo if it was in the uploads dir.
        // Security: defense-in-depth path validation — resolve real paths and
        // verify the candidate is (a) inside UPLOADS_DIR and (b) matches the
        // managed filename pattern (logo-<hex>.<ext>) before unlinking.
        try {
            const branding = brandingService.getBranding();
            if (branding.logoUrl && branding.logoUrl.startsWith('/uploads/')) {
                const baseName = path.basename(branding.logoUrl);
                // Managed logos use crypto.randomBytes(8).toString('hex') => 16 hex chars
                const managedPattern = /^logo-[0-9a-f]{16}\.(png|jpg|jpeg|gif|webp|svg)$/i;
                if (managedPattern.test(baseName)) {
                    const prev = path.resolve(UPLOADS_DIR, baseName);
                    const uploadsRoot = path.resolve(UPLOADS_DIR);
                    const newPath = path.resolve(UPLOADS_DIR, req.file.filename);
                    if (prev.startsWith(uploadsRoot + path.sep) && prev !== newPath && fs.existsSync(prev)) {
                        // Final guard: stat must be a regular file (not symlink to elsewhere)
                        const st = fs.lstatSync(prev);
                        if (st.isFile() && !st.isSymbolicLink()) {
                            fs.unlinkSync(prev);
                        }
                    }
                }
            }
        } catch (_) { /* ignore cleanup errors */ }

        await db.logAction(req.session?.userId, 'branding_logo_upload', `Uploaded logo: ${req.file.filename}`, req.ip);
        res.json({ success: true, url });
    });
});

// ── Background image upload (disk storage) ───────────────────────────────────
const bgUpload = multer({
    storage: multer.diskStorage({
        destination: (_req, _file, cb) => cb(null, UPLOADS_DIR),
        filename: (_req, file, cb) => {
            const ext = path.extname(file.originalname).toLowerCase() || '.jpg';
            const hash = crypto.randomBytes(8).toString('hex');
            cb(null, `bg-${hash}${ext}`);
        }
    }),
    limits: { fileSize: 8 * 1024 * 1024 },
    fileFilter: (_req, file, cb) => {
        const allowed = /^image\/(png|jpeg|gif|webp|svg\+xml)$/;
        if (allowed.test(file.mimetype)) {
            cb(null, true);
        } else {
            cb(new Error('Invalid file type'));
        }
    }
});

/**
 * POST /api/settings/branding/upload-background - Upload a background image.
 * Used by the console / login / agent-portal wallpaper pickers. Returns the
 * served URL; the caller decides which branding field to assign it to. Old
 * background files are not auto-removed because a single image may be shared
 * across console/login/agent backgrounds.
 */
router.post('/api/settings/branding/upload-background', requireAuth, requirePermission('branding.edit'), (req, res) => {
    bgUpload.single('background')(req, res, async (err) => {
        if (err) {
            unlinkMulterFiles(req);
            const msg = err.code === 'LIMIT_FILE_SIZE' ? 'File too large (max 8 MB)' : (err.message || 'Upload failed');
            return res.status(400).json({ success: false, error: msg });
        }
        if (!req.file) {
            return res.status(400).json({ success: false, error: 'No file provided' });
        }
        const url = `/uploads/${req.file.filename}`;
        const targetMap = {
            bgImageUrl: 'bgType',
            loginBgImageUrl: 'loginBgType',
            agentBgImageUrl: 'agentBgType',
            rdclientBgImageUrl: 'rdclientBgType'
        };
        const target = String(req.body?.target || '').trim();
        let branding = null;

        if (target && targetMap[target]) {
            const updates = {
                [target]: url,
                [targetMap[target]]: 'image'
            };
            await brandingService.saveBranding(updates);
            branding = brandingService.getBranding();
        }

        await db.logAction(
            req.session?.userId,
            'branding_bg_upload',
            target && targetMap[target]
                ? `Uploaded and applied background: ${req.file.filename} (${target})`
                : `Uploaded background: ${req.file.filename}`,
            req.ip
        );
        res.json({
            success: true,
            url,
            applied: !!branding,
            data: branding,
            revision: branding ? brandingService.getBrandingRevision() : undefined
        });
    });
});

/**
 * POST /api/settings/branding/reset - Reset branding to defaults (admin only)
 */
router.post('/api/settings/branding/reset', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        await brandingService.resetBranding();
        
        await db.logAction(req.session?.userId, 'branding_reset', 'Reset branding to defaults', req.ip);
        
        res.json({ success: true, message: 'Branding reset to defaults' });
    } catch (err) {
        console.error('Reset branding error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

/**
 * GET /api/settings/branding/export - Export branding preset as JSON
 */
router.get('/api/settings/branding/export', requireAuth, requirePermission('branding.edit'), (req, res) => {
    try {
        const preset = brandingService.exportPreset();
        res.setHeader('Content-Type', 'application/json');
        res.setHeader('Content-Disposition', 'attachment; filename="betterdesk-theme.json"');
        res.json(preset);
    } catch (err) {
        console.error('Export branding error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/branding/import - Import branding preset from JSON (admin only)
 */
router.post('/api/settings/branding/import', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const preset = req.body;
        const success = await brandingService.importPreset(preset);
        
        if (!success) {
            return res.status(400).json({ success: false, error: 'Invalid theme preset file' });
        }
        
        await db.logAction(req.session?.userId, 'branding_import', 'Imported branding preset', req.ip);
        
        res.json({ success: true, message: 'Theme imported successfully' });
    } catch (err) {
        console.error('Import branding error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

// ==================== Branding Profiles ====================

/**
 * POST /api/settings/branding/profiles - Create profile from current branding
 */
router.post('/api/settings/branding/profiles', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const name = String(req.body?.name || '').trim();
        const description = String(req.body?.description || '').trim();
        if (!name) {
            return res.status(400).json({ success: false, error: 'Profile name is required' });
        }
        let preset = null;
        if (req.body?.branding && typeof req.body.branding === 'object') {
            preset = { version: '1.0', type: 'betterdesk-theme', branding: req.body.branding };
        }
        const id = await brandingService.createProfile(name, description, preset);
        await db.logAction(req.session?.userId, 'branding_profile_create', `Created profile: ${name}`, req.ip);
        res.json({ success: true, data: { id } });
    } catch (err) {
        const msg = err.message?.includes('UNIQUE') ? 'Profile name already exists' : (err.message || req.t('errors.server_error'));
        console.error('Create branding profile error:', err);
        res.status(400).json({ success: false, error: msg });
    }
});

/**
 * PUT /api/settings/branding/profiles/:id - Update profile metadata/data
 */
router.put('/api/settings/branding/profiles/:id', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const id = parseInt(req.params.id, 10);
        if (!Number.isFinite(id)) {
            return res.status(400).json({ success: false, error: 'Invalid profile id' });
        }
        await brandingService.updateProfile(id, {
            name: req.body?.name,
            description: req.body?.description,
            branding: req.body?.branding
        });
        await db.logAction(req.session?.userId, 'branding_profile_update', `Updated profile #${id}`, req.ip);
        res.json({ success: true });
    } catch (err) {
        console.error('Update branding profile error:', err);
        res.status(400).json({ success: false, error: err.message || req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/branding/profiles/:id/apply - Apply profile to active branding
 */
router.post('/api/settings/branding/profiles/:id/apply', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const id = parseInt(req.params.id, 10);
        if (!Number.isFinite(id)) {
            return res.status(400).json({ success: false, error: 'Invalid profile id' });
        }
        const ok = await brandingService.applyProfile(id);
        if (!ok) {
            return res.status(404).json({ success: false, error: 'Profile not found or invalid' });
        }
        await db.logAction(req.session?.userId, 'branding_profile_apply', `Applied profile #${id}`, req.ip);
        res.json({ success: true, data: brandingService.getBranding(), revision: brandingService.getBrandingRevision() });
    } catch (err) {
        console.error('Apply branding profile error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/branding/profiles/:id/duplicate - Duplicate profile
 */
router.post('/api/settings/branding/profiles/:id/duplicate', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const id = parseInt(req.params.id, 10);
        if (!Number.isFinite(id)) {
            return res.status(400).json({ success: false, error: 'Invalid profile id' });
        }
        const newId = await brandingService.duplicateProfile(id, req.body?.name || '');
        res.json({ success: true, data: { id: newId } });
    } catch (err) {
        console.error('Duplicate branding profile error:', err);
        res.status(400).json({ success: false, error: err.message || req.t('errors.server_error') });
    }
});

/**
 * DELETE /api/settings/branding/profiles/:id - Delete profile
 */
router.delete('/api/settings/branding/profiles/:id', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const id = parseInt(req.params.id, 10);
        if (!Number.isFinite(id)) {
            return res.status(400).json({ success: false, error: 'Invalid profile id' });
        }
        await brandingService.deleteProfile(id);
        await db.logAction(req.session?.userId, 'branding_profile_delete', `Deleted profile #${id}`, req.ip);
        res.json({ success: true });
    } catch (err) {
        console.error('Delete branding profile error:', err);
        res.status(400).json({ success: false, error: err.message || req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/themes/:id/apply - Apply a built-in theme preset (admin only)
 */
router.post('/api/settings/themes/:id/apply', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const fs = require('fs');
        const path = require('path');
        const themeId = req.params.id;
        if (!/^[a-zA-Z0-9_-]+$/.test(themeId)) {
            return res.status(400).json({ success: false, error: 'Invalid theme id' });
        }
        const themesDir = path.resolve(__dirname, '..', 'themes');
        const themeFile = resolveChildPath(themesDir, `${themeId}.json`);

        if (!fs.existsSync(themeFile)) {
            return res.status(404).json({ success: false, error: 'Theme not found' });
        }

        const preset = JSON.parse(fs.readFileSync(themeFile, 'utf8'));
        const success = await brandingService.importPreset(preset);

        if (!success) {
            return res.status(400).json({ success: false, error: 'Invalid theme format' });
        }

        const db = require('../services/database');
        await db.logAction(req.session?.userId, 'theme_apply', `Applied theme: ${req.params.id}`, req.ip);

        res.json({ success: true, message: `Theme "${req.params.id}" applied` });
    } catch (err) {
        console.error('Apply theme error:', err);
        res.status(500).json({ success: false, error: 'Failed to apply theme' });
    }
});

// ==================== Font Management API ====================

/**
 * POST /api/settings/fonts/download - Download a Google Font to server
 * Body: { family: "Inter", weights: ["400", "500", "600", "700"] }
 */
router.post('/api/settings/fonts/download', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const { family, weights } = req.body;
        if (!family || typeof family !== 'string') {
            return res.status(400).json({ success: false, error: 'Font family is required' });
        }

        const result = await fontService.downloadFont(family, weights || ['400', '500', '600', '700']);

        await db.logAction(req.session?.userId, 'font_download', `Downloaded font: ${family} (${result.files.length} files)`, req.ip);

        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Font download error:', err);
        res.status(500).json({ success: false, error: err.message || 'Failed to download font' });
    }
});

/**
 * DELETE /api/settings/fonts/:family - Delete a locally downloaded font
 */
router.delete('/api/settings/fonts/:family', requireAuth, requirePermission('branding.edit'), async (req, res) => {
    try {
        const family = decodeURIComponent(req.params.family);
        const result = fontService.deleteLocalFont(family);

        if (result) {
            await db.logAction(req.session?.userId, 'font_delete', `Deleted font: ${family}`, req.ip);
        }

        res.json({ success: true, deleted: result });
    } catch (err) {
        console.error('Font delete error:', err);
        res.status(500).json({ success: false, error: 'Failed to delete font' });
    }
});

const fontUpload = multer({
    storage: multer.memoryStorage(),
    limits: { fileSize: 5 * 1024 * 1024 },
    fileFilter: (_req, file, cb) => {
        const ok = /\.(woff2|ttf)$/i.test(file.originalname)
            || file.mimetype === 'font/woff2'
            || file.mimetype === 'font/ttf'
            || file.mimetype === 'application/font-woff2'
            || file.mimetype === 'application/x-font-ttf';
        cb(ok ? null : new Error('Invalid font file type'), ok);
    }
});

/**
 * POST /api/settings/fonts/upload - Upload a custom font (woff2/ttf)
 */
router.post('/api/settings/fonts/upload', requireAuth, requirePermission('branding.edit'), (req, res) => {
    fontUpload.single('font')(req, res, async (err) => {
        if (err) {
            unlinkMulterFiles(req);
            const msg = err.code === 'LIMIT_FILE_SIZE' ? 'File too large (max 5 MB)' : (err.message || 'Upload failed');
            return res.status(400).json({ success: false, error: msg });
        }
        if (!req.file) {
            return res.status(400).json({ success: false, error: 'No file provided' });
        }
        try {
            const family = String(req.body?.family || path.basename(req.file.originalname, path.extname(req.file.originalname))).trim();
            const ext = path.extname(req.file.originalname).toLowerCase() === '.ttf' ? '.ttf' : '.woff2';
            const tmpDir = path.join(__dirname, '..', 'data', 'tmp');
            if (!fs.existsSync(tmpDir)) fs.mkdirSync(tmpDir, { recursive: true });
            const tmpPath = path.join(tmpDir, `font-upload-${Date.now()}${ext}`);
            fs.writeFileSync(tmpPath, req.file.buffer);
            const result = await fontService.registerUploadedFont(family, tmpPath, path.basename(tmpPath));
            await db.logAction(req.session?.userId, 'font_upload', `Uploaded font: ${family}`, req.ip);
            res.json({ success: true, data: result });
        } catch (uploadErr) {
            console.error('Font upload error:', uploadErr);
            res.status(400).json({ success: false, error: uploadErr.message || 'Upload failed' });
        }
    });
});

module.exports = router;
