package content

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"teyljunior/internal/core"
	"teyljunior/internal/store"
)

// Import записывает комплект в редакторскую базу как draft-поставку. Импорт не равен
// публикации (§6). Семейства и редакции общие для поставок: уже существующая редакция с другим
// содержимым отклоняется, потому что опубликованная редакция неизменяема.
func Import(ctx context.Context, db *store.DB, b *Bundle, now time.Time) error {
	manifest, err := json.Marshal(b.Manifest())
	if err != nil {
		return err
	}
	return db.InTx(ctx, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			INSERT INTO program_releases (version, lineage_id, status, manifest, sha256, imported_at)
			VALUES ($1, $2, 'draft', $3, $4, $5)
			ON CONFLICT (version) DO NOTHING`,
			b.Program.ProgramVersion, b.Program.LineageID, manifest, b.SHA256, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			var sha string
			if err := tx.QueryRow(ctx, `SELECT sha256 FROM program_releases WHERE version = $1`, b.Program.ProgramVersion).Scan(&sha); err != nil {
				return err
			}
			if sha != b.SHA256 {
				return fmt.Errorf("поставка %s уже импортирована с другим содержимым", b.Program.ProgramVersion)
			}
			return nil // повторный импорт того же комплекта ничего не меняет
		}
		for _, it := range b.Program.Items {
			if err := importItem(ctx, tx, b, it, now); err != nil {
				return fmt.Errorf("%s: %w", it.ItemID, err)
			}
		}
		return nil
	})
}

func importItem(ctx context.Context, tx pgx.Tx, b *Bundle, it Item, now time.Time) error {
	var outcome *string
	if it.AssessedOutcomeID != "" {
		outcome = &it.AssessedOutcomeID
	}
	var purpose, module string
	err := tx.QueryRow(ctx, `
		INSERT INTO content_families (id, purpose, module_id, topic_tags, assessed_outcome_id)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (id) DO UPDATE SET id = EXCLUDED.id
		RETURNING purpose, module_id`,
		it.FamilyID, it.Purpose, it.ModuleID, it.TopicTags, outcome).Scan(&purpose, &module)
	if err != nil {
		return err
	}
	if purpose != it.Purpose || module != it.ModuleID {
		return fmt.Errorf("семейство %s уже существует с другим назначением или модулем", it.FamilyID)
	}

	pub, err := json.Marshal(it.Public)
	if err != nil {
		return err
	}
	priv, err := json.Marshal(b.GradingOf(it))
	if err != nil {
		return err
	}
	sum := sha256.Sum256(append(append(pub, 0), priv...))
	payloadSHA := hex.EncodeToString(sum[:])
	locale := b.Program.Locale
	var existing string
	err = tx.QueryRow(ctx, `
		INSERT INTO content_revisions (item_id, revision, grading_revision, locale, family_id,
		                               public_payload, private_payload, payload_sha256, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (item_id, revision, grading_revision, locale) DO UPDATE SET item_id = EXCLUDED.item_id
		RETURNING payload_sha256`,
		it.ItemID, it.Revision, it.GradingRevision, locale, it.FamilyID, pub, priv, payloadSHA, now).Scan(&existing)
	if err != nil {
		return err
	}
	if existing != payloadSHA {
		return fmt.Errorf("редакция %d/%d уже существует с другим содержимым: нужна новая revision или gradingRevision", it.Revision, it.GradingRevision)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO release_items (program_version, item_id, revision, grading_revision, locale, family_id)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		b.Program.ProgramVersion, it.ItemID, it.Revision, it.GradingRevision, locale, it.FamilyID)
	return err
}

// Publish переводит draft-поставку в published. Требуется имя утвердившего редактора (§6).
// Перед публикацией каждая публичная часть заново разбирается строго в PublicCard: поле вне
// allowlist остановит публикацию.
func Publish(ctx context.Context, db *store.DB, version, approvedBy string, now time.Time) error {
	if approvedBy == "" {
		return errors.New("публикация требует имени утвердившего редактора")
	}
	return db.InTx(ctx, func(tx pgx.Tx) error {
		var status string
		err := tx.QueryRow(ctx, `SELECT status FROM program_releases WHERE version = $1 FOR UPDATE`, version).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("поставка %s не импортирована", version)
		}
		if err != nil {
			return err
		}
		if status != "draft" {
			return fmt.Errorf("поставка %s уже в статусе %s", version, status)
		}
		rows, err := tx.Query(ctx, `
			SELECT r.item_id, r.public_payload FROM release_items ri
			JOIN content_revisions r USING (item_id, revision, grading_revision, locale)
			WHERE ri.program_version = $1`, version)
		if err != nil {
			return err
		}
		var n int
		for rows.Next() {
			var id string
			var payload []byte
			if err := rows.Scan(&id, &payload); err != nil {
				return err
			}
			var pc PublicCard
			if err := strictUnmarshal(payload, &pc); err != nil {
				return fmt.Errorf("%s: публичная проекция не прошла проверку: %w", id, err)
			}
			n++
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("поставка %s пуста", version)
		}
		_, err = tx.Exec(ctx, `UPDATE program_releases SET status = 'published', published_at = $2, approved_by = $3 WHERE version = $1`,
			version, now, approvedBy)
		return err
	})
}

// RevisionKey — редакция, зафиксированная попыткой.
type RevisionKey struct {
	ItemID          string
	Revision        int
	GradingRevision int
	Locale          string
}

// Revision — опубликованная редакция карточки.
type Revision struct {
	RevisionKey
	FamilyID string
	Purpose  string
	ModuleID string
	Status   string
	Public   PublicCard
	Grading  Grading
}

// Catalog читает поставки и редакции. Содержимое редакции неизменяемо, поэтому кешируется;
// статус (active/withdrawn) читается каждый раз.
type Catalog struct {
	db        *store.DB
	revisions sync.Map // RevisionKey → *Revision без статуса
	manifests sync.Map // version → *Manifest
}

func NewCatalog(db *store.DB) *Catalog { return &Catalog{db: db} }

// LatestPublished — версия последней опубликованной поставки; нет — 503.
func (c *Catalog) LatestPublished(ctx context.Context, q store.Querier) (string, error) {
	var v string
	err := q.QueryRow(ctx, `SELECT version FROM program_releases WHERE status = 'published' ORDER BY published_at DESC LIMIT 1`).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", core.Unavailable("CONTENT_UNAVAILABLE", "учебная программа ещё не опубликована")
	}
	return v, err
}

// Manifest — манифест поставки.
func (c *Catalog) Manifest(ctx context.Context, q store.Querier, version string) (*Manifest, error) {
	if m, ok := c.manifests.Load(version); ok {
		return m.(*Manifest), nil
	}
	var raw []byte
	if err := q.QueryRow(ctx, `SELECT manifest FROM program_releases WHERE version = $1`, version).Scan(&raw); err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	c.manifests.Store(version, &m)
	return &m, nil
}

// ReleaseItems — редакции поставки по семействам.
func (c *Catalog) ReleaseItems(ctx context.Context, q store.Querier, version string) ([]RevisionKey, []string, error) {
	rows, err := q.Query(ctx, `
		SELECT item_id, revision, grading_revision, locale, family_id FROM release_items
		WHERE program_version = $1 ORDER BY item_id`, version)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var keys []RevisionKey
	var families []string
	for rows.Next() {
		var k RevisionKey
		var f string
		if err := rows.Scan(&k.ItemID, &k.Revision, &k.GradingRevision, &k.Locale, &f); err != nil {
			return nil, nil, err
		}
		keys = append(keys, k)
		families = append(families, f)
	}
	return keys, families, rows.Err()
}

// ItemForFamily — редакция семейства в поставке.
func (c *Catalog) ItemForFamily(ctx context.Context, q store.Querier, version, familyID string) (RevisionKey, error) {
	var k RevisionKey
	err := q.QueryRow(ctx, `
		SELECT item_id, revision, grading_revision, locale FROM release_items
		WHERE program_version = $1 AND family_id = $2`, version, familyID).
		Scan(&k.ItemID, &k.Revision, &k.GradingRevision, &k.Locale)
	if errors.Is(err, pgx.ErrNoRows) {
		return k, core.NotFound()
	}
	return k, err
}

// Revision читает редакцию с актуальным статусом.
func (c *Catalog) Revision(ctx context.Context, q store.Querier, k RevisionKey) (*Revision, error) {
	var status string
	if v, ok := c.revisions.Load(k); ok {
		if err := q.QueryRow(ctx, `SELECT status FROM content_revisions WHERE item_id = $1 AND revision = $2 AND grading_revision = $3 AND locale = $4`,
			k.ItemID, k.Revision, k.GradingRevision, k.Locale).Scan(&status); err != nil {
			return nil, err
		}
		r := *v.(*Revision)
		r.Status = status
		return &r, nil
	}
	var pub, priv []byte
	r := Revision{RevisionKey: k}
	err := q.QueryRow(ctx, `
		SELECT r.family_id, f.purpose, f.module_id, r.status, r.public_payload, r.private_payload
		FROM content_revisions r JOIN content_families f ON f.id = r.family_id
		WHERE r.item_id = $1 AND r.revision = $2 AND r.grading_revision = $3 AND r.locale = $4`,
		k.ItemID, k.Revision, k.GradingRevision, k.Locale).Scan(&r.FamilyID, &r.Purpose, &r.ModuleID, &r.Status, &pub, &priv)
	if err != nil {
		return nil, err
	}
	if err := strictUnmarshal(pub, &r.Public); err != nil {
		return nil, fmt.Errorf("public payload %s: %w", k.ItemID, err)
	}
	if err := json.NewDecoder(bytes.NewReader(priv)).Decode(&r.Grading); err != nil {
		return nil, fmt.Errorf("private payload %s: %w", k.ItemID, err)
	}
	cached := r
	cached.Status = ""
	c.revisions.Store(k, &cached)
	return &r, nil
}
