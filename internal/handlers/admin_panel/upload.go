// CLAUDE:SUMMARY Upload fichier branding : multipart POST, mime check, sha256 prefix, stockage BRANDING_DIR/uploads/ (M-ASSOKIT-ADMIN-PANEL-V0).
package adminpanel

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/hazyhaar/assokit/internal/app"
	"github.com/hazyhaar/assokit/pkg/horui/branding"
	"github.com/hazyhaar/assokit/pkg/horui/middleware"
)

const defaultMaxUpload = 5 * 1024 * 1024 // 5 MB par défaut

// brandingDir retourne le répertoire de stockage des fichiers uploadés.
func brandingDirPath() string {
	d := os.Getenv("BRANDING_DIR")
	if d == "" {
		return "./uploads"
	}
	return d
}

// AdminPanelUpload est le handler POST /admin/panel/upload-file.
func AdminPanelUpload(deps app.AppDeps, fields []Field) http.HandlerFunc {
	return HandleUploadFile(deps, brandingDirPath())
}

// AdminPanelDeleteFile est le handler POST /admin/panel/delete-file.
func AdminPanelDeleteFile(deps app.AppDeps, fields []Field) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "form invalide", http.StatusBadRequest)
			return
		}
		key := strings.TrimSpace(r.FormValue("key"))
		if key == "" {
			http.Error(w, "key manquant", http.StatusBadRequest)
			return
		}

		if row, ok := branding.GetRow(r.Context(), deps.DB, key); ok && row.FilePath != "" {
			removeFile(row.FilePath)
		}

		if err := branding.DeleteFile(r.Context(), deps.DB, key); err != nil {
			http.Error(w, "erreur suppression", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<span class="field-badge field-badge--empty">Fichier supprimé</span>`)) //nolint:errcheck
	}
}

// HandleUploadFile — POST /admin/panel/upload-file : stocke fichier, met à jour branding_kv.
func HandleUploadFile(deps app.AppDeps, brandingDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.FormValue("key"))
		if key == "" {
			http.Error(w, "key manquant", http.StatusBadRequest)
			return
		}

		field, ok := fieldByKey(key)
		if !ok || field.Kind != "file" {
			http.Error(w, "champ inconnu ou non-file", http.StatusBadRequest)
			return
		}

		maxBytes := int64(defaultMaxUpload)
		if field.MaxBytes > 0 {
			maxBytes = int64(field.MaxBytes)
		}

		r.Body = http.MaxBytesReader(w, r.Body, maxBytes+1024)
		if err := r.ParseMultipartForm(maxBytes); err != nil {
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write([]byte(`<span class="field-badge field-badge--error">Fichier trop volumineux</span>`)) //nolint:errcheck
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "fichier manquant", http.StatusBadRequest)
			return
		}
		defer file.Close()

		// Mime check via contenu réel
		buf := make([]byte, 512)
		n, _ := file.Read(buf)
		ctx := r.Context()
		reqID := middleware.RequestIDFromContext(ctx)

		mime := http.DetectContentType(buf[:n])
		if len(field.MimeAllow) > 0 && !mimeAllowed(mime, field.MimeAllow) {
			// Tolérance SVG : DetectContentType retourne text/plain pour SVG
			if !(slices.Contains(field.MimeAllow, "image/svg+xml") && looksLikeSVG(buf[:n])) {
				deps.Logger.Warn("admin_panel_upload_mime_mismatch",
					"req_id", reqID,
					"key", key,
					"declared_filename", header.Filename,
					"detected_mime", mime,
					"allowed", field.MimeAllow,
				)
				w.WriteHeader(http.StatusUnsupportedMediaType)
				w.Write([]byte(`<span class="field-badge field-badge--error">Type de fichier non autorisé</span>`)) //nolint:errcheck
				return
			}
			mime = "image/svg+xml"
		}

		// Rembobiner pour relire le fichier entier
		if _, err := file.Seek(0, io.SeekStart); err != nil {
			http.Error(w, "erreur lecture fichier", http.StatusInternalServerError)
			return
		}

		// Lire le contenu complet pour hash + stockage
		content, err := io.ReadAll(file)
		if err != nil {
			http.Error(w, "erreur lecture fichier", http.StatusInternalServerError)
			return
		}

		hash := sha256.Sum256(content)
		prefix := fmt.Sprintf("%x", hash[:4])
		origName := sanitizeFilename(header.Filename)
		fileName := prefix + "-" + origName

		uploadsDir := filepath.Join(brandingDir, "uploads")
		if err := os.MkdirAll(uploadsDir, 0750); err != nil {
			http.Error(w, "erreur répertoire", http.StatusInternalServerError)
			return
		}

		destPath := filepath.Join(uploadsDir, fileName)
		if err := os.WriteFile(destPath, content, 0640); err != nil {
			http.Error(w, "erreur écriture fichier", http.StatusInternalServerError)
			return
		}

		// Supprimer l'ancien fichier si différent
		if row, ok := branding.GetRow(r.Context(), deps.DB, key); ok && row.FilePath != "" && row.FilePath != destPath {
			removeFile(row.FilePath)
		}

		userID := ""
		if u := middleware.UserFromContext(ctx); u != nil {
			userID = u.ID
		}

		if err := branding.SetFile(ctx, deps.DB, key, destPath, mime, userID, int64(len(content))); err != nil {
			deps.Logger.Error("admin_panel_file_save_failed",
				"req_id", reqID,
				"user_id", userID,
				"key", key,
				"err", err.Error(),
			)
			http.Error(w, "erreur sauvegarde", http.StatusInternalServerError)
			return
		}

		deps.Logger.Info("admin_panel_file_uploaded",
			"req_id", reqID,
			"user_id", userID,
			"key", key,
			"mime", mime,
			"size", int64(len(content)),
		)

		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<span class="field-badge field-badge--saved">✓ Fichier enregistré</span>`)) //nolint:errcheck
	}
}

// removeFile supprime silencieusement un fichier.
func removeFile(path string) {
	if path != "" {
		os.Remove(path) //nolint:errcheck
	}
}

func mimeAllowed(mime string, allowed []string) bool {
	// Normaliser en enlevant les paramètres (charset=...)
	if i := strings.Index(mime, ";"); i > 0 {
		mime = strings.TrimSpace(mime[:i])
	}
	return slices.Contains(allowed, mime)
}

func looksLikeSVG(buf []byte) bool {
	s := strings.TrimSpace(string(buf))
	return strings.HasPrefix(s, "<svg") || strings.HasPrefix(s, "<?xml") || strings.Contains(s, "<svg")
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	var sb strings.Builder
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '.' || r == '-' || r == '_' {
			sb.WriteRune(r)
		} else {
			sb.WriteRune('_')
		}
	}
	s := sb.String()
	if s == "" {
		return "upload"
	}
	return s
}
