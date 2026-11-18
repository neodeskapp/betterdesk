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
const updateService = require('../services/updateService');
const { resolveChildPath } = require('../lib/safePath');
const { unlinkMulterFiles } = require('../lib/multerCleanup');
const { canScheduleConsoleRestart } = require('../lib/updateFailurePolicy');
const {
    persistUpdateResult,
    clearLastUpdateResult,
    resolveLastUpdateResultForDisplay,
} = require('../lib/updateResultStore');
const { splitUpdateFailures } = require('../lib/updateFailurePolicy');
const restartCoordinator = require('../services/restartCoordinator');
const { getSmtpSettings, putSmtpSettings, testSmtpSettings } = require('../lib/smtpSettingsHandlers');
const { canUsePrivilegedUpdate, invokePrivilegedUpdate } = require('../lib/privilegedUpdateHelper');
const { requireAuth, requirePermission } = require('../middleware/auth');
const managementCapabilities = require('../lib/managementCapabilities');
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

// ==================== Self-Update API ====================

/**
 * GET /api/settings/updates/server-info - Check Go build environment for server updates
 */
router.get('/api/settings/updates/server-info', requireAuth, requirePermission('server.config'), async (_req, res) => {
    try {
        const info = updateService.getServerUpdateInfo();
        res.json({ success: true, data: info });
    } catch (err) {
        console.error('Server info error:', err);
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * POST /api/settings/updates/install-go - Download & install the Go toolchain
 * into data/go-toolchain/ so the console can compile the Go server even when
 * Go is not present on the host. Idempotent.
 */
router.post('/api/settings/updates/install-go', requireAuth, requirePermission('server.config'), async (req, res) => {
    req.setTimeout(600000);
    res.setTimeout(600000);
    try {
        const result = await updateService.installGoToolchain();
        await db.logAction(
            req.session?.userId,
            'system_install_go',
            result.success ? `Installed Go toolchain (${result.version})` : `Go toolchain install failed: ${result.error}`,
            req.ip
        );
        if (!result.success) {
            return res.status(500).json({ success: false, error: result.error || 'Toolchain install failed' });
        }
        res.json({ success: true, data: { version: result.version, binPath: result.binPath } });
    } catch (err) {
        console.error('Go toolchain install error:', err);
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * GET /api/settings/updates/preflight - Pre-install environment checks (issue #158)
 */
router.get('/api/settings/updates/preflight', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const serverUpdateRequired = req.query.serverUpdate === '1' || req.query.serverUpdate === 'true';
        const remoteSHA = typeof req.query.sha === 'string' ? req.query.sha : null;
        const result = await updateService.runUpdatePreflight({ serverUpdateRequired, remoteSHA });
        result.capabilities = managementCapabilities.getCapabilityReport();
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Update preflight error:', err);
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * GET /api/settings/management/capabilities
 *
 * Read-only host capability report used by the panel and repair tools.
 * Secrets and environment values are intentionally not included here.
 */
router.get('/api/settings/management/capabilities', requireAuth, requirePermission('server.config'), (_req, res) => {
    try {
        res.json({ success: true, data: managementCapabilities.getCapabilityReport() });
    } catch (err) {
        console.error('Management capability check error:', err);
        res.status(500).json({ success: false, error: err.message || 'capability_check_failed' });
    }
});

/**
 * GET /api/settings/management/config
 *
 * Return allowlisted .env values with secrets masked. The example file is the
 * source of truth for the key set, so updates automatically remain aligned
 * with installer-generated configuration.
 */
router.get('/api/settings/management/config', requireAuth, requirePermission('server.config'), (_req, res) => {
    try {
        const paths = managementCapabilities.resolvePaths();
        const values = managementCapabilities.readEnvFile(paths.envPath);
        const keys = [...managementCapabilities.getAllowedEnvKeys(paths)].sort();
        const data = {};
        for (const key of keys) {
            if (Object.prototype.hasOwnProperty.call(values, key)) {
                data[key] = managementCapabilities.maskValue(key, values[key]);
            }
        }
        res.json({ success: true, data: { keys, values: data, envPath: paths.envPath } });
    } catch (err) {
        console.error('Management configuration read error:', err);
        res.status(500).json({ success: false, error: err.message || 'configuration_read_failed' });
    }
});

/**
 * PUT /api/settings/management/config
 *
 * Body: { changes: { KEY: "value" }, dryRun?: boolean }
 * Changes are backed up and written atomically. A restart transaction is
 * returned so the operator can confirm or cancel the change in the panel.
 */
router.put('/api/settings/management/config', requireAuth, requirePermission('server.config'), async (req, res) => {
    let result = null;
    try {
        const changes = req.body && req.body.changes;
        const validated = managementCapabilities.validateEnvChanges(changes);
        const capabilities = managementCapabilities.getCapabilityReport();
        if (req.body && (req.body.dryRun === true || req.body.dryRun === 'true')) {
            return res.json({
                success: true,
                data: {
                    dryRun: true,
                    changes: Object.fromEntries(Object.entries(validated).map(([key, value]) => [
                        key,
                        managementCapabilities.maskValue(key, value),
                    ])),
                    capabilities,
                },
            });
        }
        const canUseBroker = process.platform === 'linux' && canUsePrivilegedUpdate();
        if (!capabilities.groups.config.ready && !canUseBroker) {
            return res.status(409).json({
                success: false,
                error: 'Configuration path is not writable',
                code: 'CONFIG_CAPABILITY_BLOCKED',
                data: { capabilities },
            });
        }

        if (capabilities.groups.config.ready) {
            result = managementCapabilities.applyEnvChanges(validated);
        } else {
            const paths = managementCapabilities.resolvePaths();
            result = invokePrivilegedUpdate({
                action: 'write_env',
                path: paths.envPath,
                vars: validated,
            });
            result.envPath = paths.envPath;
            result.changed = result.keys || Object.keys(validated);
        }
        const pending = restartCoordinator.registerChange(req, {
            key: `managed-env:${result.changed.join(',')}`,
            label: `Managed configuration: ${result.changed.join(', ')}`,
            rollback: {
                type: 'env-config',
                backupPath: result.backupPath,
                options: managementCapabilities.resolvePaths(),
            },
        });
        await db.logAction(
            req.session?.userId,
            'managed_config_saved',
            `Saved allowlisted environment keys: ${result.changed.join(', ')}`,
            req.ip,
        );
        res.json({
            success: true,
            data: {
                ...result,
                pendingRestart: pending,
            },
            message: 'Configuration saved; service restart confirmation is required',
        });
    } catch (err) {
        if (result && result.backupPath) {
            try {
                managementCapabilities.restoreEnvBackup(
                    result.backupPath,
                    managementCapabilities.resolvePaths(),
                );
            } catch (rollbackErr) {
                try {
                    invokePrivilegedUpdate({
                        action: 'restore_env',
                        path: managementCapabilities.resolvePaths().envPath,
                        backupPath: result.backupPath,
                    });
                } catch (brokerErr) {
                    console.error('Managed configuration rollback error:', brokerErr || rollbackErr);
                }
            }
        }
        const validationError = /allowlisted|Invalid |No configuration|control character|boolean|port|outside BetterDesk|absolute/i.test(err.message || '');
        res.status(validationError ? 400 : 500).json({
            success: false,
            error: err.message || 'configuration_write_failed',
        });
    }
});

/**
 * GET /api/settings/updates/channel - Current GitHub update channel (stable/dev)
 */
router.get('/api/settings/updates/channel', requireAuth, requirePermission('server.config'), (_req, res) => {
    try {
        const data = updateService.getUpdateChannelInfo();
        res.json({ success: true, data });
    } catch (err) {
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * POST /api/settings/updates/channel - Switch update channel (writes .env)
 * Body: { channel: 'stable' | 'development' }
 */
router.post('/api/settings/updates/channel', requireAuth, requirePermission('server.config'), (req, res) => {
    try {
        const { channel } = req.body || {};
        if (!channel || !updateService.UPDATE_CHANNELS[channel]) {
            return res.status(400).json({
                success: false,
                error: 'Invalid channel. Use stable or development.'
            });
        }
        const result = updateService.setUpdateChannel(channel);
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Update channel error:', err);
        const code = err.code === 'DOCKER_IMAGE_CHANNEL' ? 'DOCKER_IMAGE_CHANNEL' : undefined;
        res.status(code ? 400 : 500).json({
            success: false,
            error: err.message,
            code,
        });
    }
});

/**
 * GET /api/settings/updates/check - Check for available updates
 */
router.get('/api/settings/updates/check', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const result = await updateService.checkForUpdates();
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Update check error:', err);
        const rateLimited = updateService.isGithubRateLimitError(err);
        res.status(rateLimited ? 503 : 500).json({
            success: false,
            error: 'Failed to check for updates: ' + err.message,
            code: rateLimited ? 'GITHUB_RATE_LIMIT' : undefined,
        });
    }
});

/**
 * GET /api/settings/updates/last-result - Last in-app update outcome (survives closed modal)
 */
router.get('/api/settings/updates/last-result', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        let remoteSHA = null;
        try {
            const remote = await updateService.getRemoteHeadSHA();
            remoteSHA = remote?.sha || null;
        } catch (_) { /* optional for stale-banner pruning */ }

        const data = resolveLastUpdateResultForDisplay(config.dataDir, {
            rootDir: path.join(__dirname, '..'),
            localSHA: updateService.getLocalSHA(),
            remoteSHA,
        });
        res.json({ success: true, data: data || null });
    } catch (err) {
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * GET /api/settings/updates/install-status - Live install progress (#464).
 * Used when the browser loses the long-running install HTTP call (proxy timeout)
 * so the UI can keep polling until applyUpdate finishes.
 */
router.get('/api/settings/updates/install-status', requireAuth, requirePermission('server.config'), (req, res) => {
    try {
        res.json({ success: true, data: updateService.getUpdateInstallStatus() });
    } catch (err) {
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * GET /api/settings/updates/changes - Get list of changed files between local SHA and remote
 */
router.get('/api/settings/updates/changes', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const { sha } = req.query;
        if (!sha || !/^[0-9a-f]{7,40}$/i.test(sha)) {
            return res.status(400).json({ success: false, error: 'Valid SHA parameter required' });
        }
        const result = await updateService.getChangedFiles(sha);
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Get changes error:', err);
        const rateLimited = updateService.isGithubRateLimitError(err);
        res.status(rateLimited ? 503 : 500).json({
            success: false,
            error: 'Failed to get changed files: ' + err.message,
            code: rateLimited ? 'GITHUB_RATE_LIMIT' : undefined,
        });
    }
});

/**
 * POST /api/settings/updates/install - Apply the update
 * Body: { remoteSHA, createBackup: true/false }
 * The update scope is detected server-side from the changed files. Supported
 * changed components are always applied together so operators cannot
 * accidentally skip the Go server when its source changed.
 */
router.post('/api/settings/updates/install', requireAuth, requirePermission('server.config'), async (req, res) => {
    // Extend timeout for server compilation (up to 10 minutes)
    req.setTimeout(600000);
    res.setTimeout(600000);

    try {
        if (updateService.isImageBasedDockerDeployment()) {
            const dockerUpdate = updateService.getDockerUpdateInstructions();
            return res.status(400).json({
                success: false,
                error: `In-app updates are disabled in Docker image deployments. Run: ${dockerUpdate.commands.join(' && ')}`,
                dockerImageMode: true,
                dockerUpdate
            });
        }

        const { remoteSHA, createBackup } = req.body;
        if (!remoteSHA || !/^[0-9a-f]{7,40}$/i.test(remoteSHA)) {
            return res.status(400).json({ success: false, error: 'Valid remoteSHA is required' });
        }

        // Get changed files
        const changedData = await updateService.getChangedFiles(remoteSHA);
        if (changedData.totalFiles === 0) {
            return res.json({ success: true, data: { applied: [], message: 'No files to update' } });
        }

        // Apply update
        const result = await updateService.applyUpdate(remoteSHA, changedData, {
            createBackup: createBackup !== false,
            serverStrategy: 'auto'
        });

        await db.logAction(
            req.session?.userId,
            'system_update',
            `Updated to ${remoteSHA.slice(0, 7)} (${result.applied.length} applied, ${result.failed.length} failed)`
                + (result.failed.length
                    ? ` — failed: ${result.failed.map(f => `${f.file}: ${f.error || ''}`).join('; ')}`
                    : '')
                + (result.servicesFailed?.length
                    ? ` — services: ${result.servicesFailed.map(s => `${s.service}: ${s.error || ''}`).join('; ')}`
                    : ''),
            req.ip
        );

        // Sync Linux permissions/sudoers from disk before service restarts.
        if (process.platform === 'linux') {
            try {
                const modPath = require.resolve('../scripts/linux-ensure-console-user');
                delete require.cache[modPath];
                require('../scripts/linux-ensure-console-user').ensureLinuxConsoleServiceUser();
            } catch (syncErr) {
                console.warn(`[UPDATE] Pre-restart permission sync warning: ${syncErr.message}`);
            }
        }

        // Restart Go server when its binary was updated.
        if (result.needsServerRestart) {
            const serviceName = process.platform === 'win32' ? 'BetterDeskServer' : 'betterdesk-server';
            let svc = updateService.restartService(serviceName);
            if (!svc.success) {
                svc = updateService.restartService(serviceName);
            }
            if (svc.success) result.servicesRestarted.push('server');
            else {
                const fail = { service: 'server', error: svc.error };
                if (svc.nonCritical) fail.nonCritical = true;
                if (svc.hint) fail.hint = svc.hint;
                result.servicesFailed.push(fail);
            }
        }

        // Restart console after response is sent (systemd/NSSM restarts automatically)
        let scheduleConsoleRestart = false;
        if (result.needsConsoleRestart) {
            const restartGate = canScheduleConsoleRestart(result, config.dataDir);
            if (!restartGate.allowed) {
                result.consoleRestartBlocked = restartGate.blockedReason
                    || 'Console service user permissions were not verified before restart';
                console.warn(
                    '[UPDATE] Skipping console restart — service user permissions not verified.'
                    + ` Run as root: node ${path.join(__dirname, '..', 'scripts/linux-ensure-console-user.js')}`
                    + ' && systemctl restart betterdesk-console'
                );
            } else {
                if (restartGate.note) result.consoleRestartNote = restartGate.note;
                scheduleConsoleRestart = true;
            }
        }

        try {
            const rootDir = path.join(__dirname, '..');
            const { critical: criticalFailures } = splitUpdateFailures(result.failed || [], rootDir);
            // Access-denied NSSM restarts are non-critical on Windows (#272).
            const servicesFailed = (result.servicesFailed || []).filter(s => !s.nonCritical);
            const consoleRestartBlocked = result.consoleRestartBlocked || null;
            if (criticalFailures.length === 0 && servicesFailed.length === 0 && !consoleRestartBlocked) {
                clearLastUpdateResult(config.dataDir);
            } else {
                persistUpdateResult(config.dataDir, {
                    sha: remoteSHA,
                    applied: result.applied?.length || 0,
                    failed: criticalFailures,
                    servicesFailed,
                    servicesRestarted: result.servicesRestarted || [],
                    shaSaved: result.shaSaved,
                    consoleRestartBlocked,
                    consoleRestartNote: result.consoleRestartNote || null,
                    serverBuild: result.serverBuild || null,
                    serverDeploy: result.serverDeploy || null,
                });
            }
        } catch (persistErr) {
            console.warn(`[UPDATE] Could not persist last update result: ${persistErr.message}`);
        }

        res.json({ success: true, data: result });

        if (scheduleConsoleRestart) {
            setTimeout(() => {
                console.log(`[UPDATE] Restarting console after update to ${remoteSHA.slice(0, 7)}...`);
                process.exit(0);
            }, 2000);
        }
    } catch (err) {
        console.error('Update install error:', err);
        res.status(500).json({ success: false, error: 'Update failed: ' + err.message });
    }
});

/**
 * GET /api/settings/updates/server-binary/status - Report whether the running
 * Go server binary is out of date (server source updated but binary not
 * rebuilt). Lets the panel surface a security warning instead of silently
 * reporting "up to date".
 */
router.get('/api/settings/updates/server-binary/status', requireAuth, requirePermission('server.config'), (req, res) => {
    try {
        const status = updateService.getServerBinaryStatus();
        res.json({ success: true, data: status });
    } catch (err) {
        console.error('Server binary status error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/updates/server-binary/rebuild - Explicitly rebuild the Go
 * server binary from local source with the current dependency versions
 * (go.mod/go.sum) and deploy it. Bootstraps a vendored Go toolchain when one
 * is not installed.
 */
router.post('/api/settings/updates/server-binary/rebuild', requireAuth, requirePermission('server.config'), async (req, res) => {
    // Compilation may take several minutes.
    req.setTimeout(600000);
    res.setTimeout(600000);
    try {
        const result = await updateService.rebuildServerBinary({ restart: true });
        await db.logAction(
            req.session?.userId,
            'system_update',
            `Rebuilt server binary (${result.success ? 'success' : 'failed: ' + (result.error || 'unknown')})`,
            req.ip
        );
        if (!result.success) {
            return res.status(500).json({ success: false, error: result.error || 'Rebuild failed', data: result });
        }
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Server binary rebuild error:', err);
        res.status(500).json({ success: false, error: 'Rebuild failed: ' + err.message });
    }
});

/**
 * GET /api/settings/updates/backups - List pre-update backups
 */
router.get('/api/settings/updates/backups', requireAuth, requirePermission('server.config'), (req, res) => {
    try {
        const backups = updateService.listBackups();
        res.json({ success: true, data: backups });
    } catch (err) {
        console.error('List backups error:', err);
        res.status(500).json({ success: false, error: req.t('errors.server_error') });
    }
});

/**
 * POST /api/settings/updates/restore - Restore from pre-update backup
 * Body: { backupName }
 */
router.post('/api/settings/updates/restore', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const { backupName } = req.body;
        if (!backupName || typeof backupName !== 'string') {
            return res.status(400).json({ success: false, error: 'backupName is required' });
        }

        const result = updateService.restoreFromBackup(backupName);

        await db.logAction(
            req.session?.userId,
            'system_restore',
            `Restored from backup: ${backupName} (v${result.version}, ${result.restored} files)`,
            req.ip
        );

        res.json({ success: true, data: result });

        // Restart after restore
        setTimeout(() => {
            console.log(`[UPDATE] Restarting after restore from ${backupName}...`);
            process.exit(0);
        }, 2000);
    } catch (err) {
        console.error('Restore error:', err);
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * DELETE /api/settings/updates/backups/:name - Delete a single pre-update backup
 */
router.delete('/api/settings/updates/backups/:name', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const name = req.params.name;
        const result = updateService.deleteBackup(name);
        await db.logAction(req.session?.userId, 'backup_deleted', `Deleted update backup: ${name}`, req.ip);
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Delete backup error:', err);
        // 400 for validation errors, 500 for unexpected ones
        const status = /invalid|not found|outside/i.test(err.message) ? 400 : 500;
        res.status(status).json({ success: false, error: err.message });
    }
});

/**
 * POST /api/settings/updates/backups/prune - Apply retention now
 * Body: { keep: number } — keep N newest, delete the rest
 */
router.post('/api/settings/updates/backups/prune', requireAuth, requirePermission('server.config'), async (req, res) => {
    try {
        const keep = parseInt(req.body?.keep, 10);
        if (!Number.isFinite(keep) || keep <= 0) {
            return res.status(400).json({ success: false, error: 'keep must be a positive integer' });
        }
        const result = updateService.pruneBackups(keep);
        if (result.deleted.length) {
            await db.logAction(
                req.session?.userId, 'backup_pruned',
                `Manual prune: kept ${keep}, deleted ${result.deleted.length} backup(s)`,
                req.ip
            );
        }
        res.json({ success: true, data: result });
    } catch (err) {
        console.error('Prune backups error:', err);
        res.status(500).json({ success: false, error: err.message });
    }
});

/**
 * GET /api/settings/email/smtp — SMTP configuration (masked secrets).
 */
router.get('/api/settings/email/smtp', requireAuth, requirePermission('server.config'), getSmtpSettings);

/**
 * PUT /api/settings/email/smtp — Update SMTP configuration.
 */
router.put('/api/settings/email/smtp', requireAuth, requirePermission('server.config'), putSmtpSettings);

/**
 * POST /api/settings/email/smtp/test — Test SMTP connection.
 */
router.post('/api/settings/email/smtp/test', requireAuth, requirePermission('server.config'), testSmtpSettings);

module.exports = router;
