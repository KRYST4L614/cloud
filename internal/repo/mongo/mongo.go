package mongo

import (
	"context"
	"errors"
	"regexp"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	mongodriver "go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"

	"cloud/internal/domain"
)

type userDocument struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
	Email        string        `bson:"email" json:"email"`
	PasswordHash string        `bson:"password_hash" json:"-"`
	QuotaBytes   int64         `bson:"quota_bytes" json:"quota_bytes"`
	UsedBytes    int64         `bson:"used_bytes" json:"used_bytes"`
	CreatedAt    time.Time     `bson:"created_at" json:"created_at"`
}

type fileDocument struct {
	ID          bson.ObjectID `bson:"_id,omitempty" json:"id"`
	OwnerID     bson.ObjectID `bson:"owner_id" json:"-"`
	Name        string        `bson:"name" json:"name"`
	Size        int64         `bson:"size" json:"size"`
	ContentType string        `bson:"content_type" json:"content_type"`
	ObjectKey   string        `bson:"object_key" json:"-"`
	ShareToken  string        `bson:"share_token,omitempty" json:"share_token,omitempty"`
	CreatedAt   time.Time     `bson:"created_at" json:"created_at"`
}

type alertDocument struct {
	ID          bson.ObjectID     `bson:"_id,omitempty" json:"id"`
	Fingerprint string            `bson:"fingerprint" json:"fingerprint"`
	Name        string            `bson:"name" json:"name"`
	Status      string            `bson:"status" json:"status"`
	Severity    string            `bson:"severity" json:"severity"`
	Summary     string            `bson:"summary" json:"summary"`
	Description string            `bson:"description" json:"description"`
	Labels      map[string]string `bson:"labels" json:"labels"`
	StartsAt    time.Time         `bson:"starts_at" json:"starts_at"`
	EndsAt      time.Time         `bson:"ends_at" json:"ends_at"`
	UpdatedAt   time.Time         `bson:"updated_at" json:"updated_at"`
}

type DB struct {
	client *mongodriver.Client
	users  *mongodriver.Collection
	files  *mongodriver.Collection
	alerts *mongodriver.Collection
}

func Connect(ctx context.Context, uri, name string) (*DB, error) {
	client, err := mongodriver.Connect(options.Client().ApplyURI(uri).SetServerSelectionTimeout(5 * time.Second))
	if err != nil {
		return nil, err
	}
	d := client.Database(name)
	db := &DB{
		client: client,
		users:  d.Collection("users"),
		files:  d.Collection("files"),
		alerts: d.Collection("alerts"),
	}
	return db, db.ensureIndexes(ctx)
}

func (d *DB) ensureIndexes(ctx context.Context) error {
	if _, err := d.users.Indexes().CreateOne(ctx, mongodriver.IndexModel{
		Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true),
	}); err != nil {
		return err
	}
	if _, err := d.files.Indexes().CreateMany(ctx, []mongodriver.IndexModel{
		{Keys: bson.D{{Key: "owner_id", Value: 1}, {Key: "created_at", Value: -1}}},
		{Keys: bson.D{{Key: "share_token", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "object_key", Value: 1}}, Options: options.Index().SetUnique(true)},
	}); err != nil {
		return err
	}
	_, err := d.alerts.Indexes().CreateMany(ctx, []mongodriver.IndexModel{
		{Keys: bson.D{{Key: "fingerprint", Value: 1}, {Key: "starts_at", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "updated_at", Value: -1}}},
	})
	return err
}

func (d *DB) Close(ctx context.Context) error { return d.client.Disconnect(ctx) }

func (d *DB) Ping(ctx context.Context) error { return d.client.Ping(ctx, readpref.Primary()) }

func (d *DB) createUserDocument(ctx context.Context, u *userDocument) error {
	u.CreatedAt = time.Now().UTC()
	res, err := d.users.InsertOne(ctx, u)
	if mongodriver.IsDuplicateKeyError(err) {
		return domain.ErrDuplicate
	}
	if err != nil {
		return err
	}
	u.ID = res.InsertedID.(bson.ObjectID)
	return nil
}

func (d *DB) userByEmailDocument(ctx context.Context, email string) (*userDocument, error) {
	return findOne[userDocument](ctx, d.users, bson.M{"email": email})
}

func (d *DB) userByIDDocument(ctx context.Context, id bson.ObjectID) (*userDocument, error) {
	return findOne[userDocument](ctx, d.users, bson.M{"_id": id})
}

func (d *DB) reserveSpaceDocument(ctx context.Context, userID bson.ObjectID, size int64) error {
	res, err := d.users.UpdateOne(ctx,
		bson.M{"_id": userID, "$expr": bson.M{"$lte": bson.A{bson.M{"$add": bson.A{"$used_bytes", size}}, "$quota_bytes"}}},
		bson.M{"$inc": bson.M{"used_bytes": size}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return domain.ErrQuotaExceeded
	}
	return nil
}

func (d *DB) releaseSpaceDocument(ctx context.Context, userID bson.ObjectID, size int64) error {
	_, err := d.users.UpdateOne(ctx, bson.M{"_id": userID}, bson.M{"$inc": bson.M{"used_bytes": -size}})
	return err
}

func (d *DB) createFileDocument(ctx context.Context, f *fileDocument) error {
	_, err := d.files.InsertOne(ctx, f)
	return err
}

func (d *DB) listFilesDocument(ctx context.Context, owner bson.ObjectID, search string) ([]fileDocument, error) {
	filter := bson.M{"owner_id": owner}
	if search != "" {
		filter["name"] = bson.M{"$regex": bson.Regex{Pattern: regexp.QuoteMeta(search), Options: "i"}}
	}
	cur, err := d.files.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "created_at", Value: -1}}).SetLimit(1000))
	if err != nil {
		return nil, err
	}
	files := []fileDocument{}
	return files, cur.All(ctx, &files)
}

func (d *DB) fileByIDDocument(ctx context.Context, owner, id bson.ObjectID) (*fileDocument, error) {
	return findOne[fileDocument](ctx, d.files, bson.M{"_id": id, "owner_id": owner})
}

func (d *DB) fileByShareTokenDocument(ctx context.Context, token string) (*fileDocument, error) {
	return findOne[fileDocument](ctx, d.files, bson.M{"share_token": token})
}

func (d *DB) renameFileDocument(ctx context.Context, owner, id bson.ObjectID, name string) error {
	return d.updateFile(ctx, owner, id, bson.M{"$set": bson.M{"name": name}})
}

func (d *DB) setShareTokenDocument(ctx context.Context, owner, id bson.ObjectID, token string) error {
	if token == "" {
		return d.updateFile(ctx, owner, id, bson.M{"$unset": bson.M{"share_token": ""}})
	}
	return d.updateFile(ctx, owner, id, bson.M{"$set": bson.M{"share_token": token}})
}

func (d *DB) updateFile(ctx context.Context, owner, id bson.ObjectID, update bson.M) error {
	res, err := d.files.UpdateOne(ctx, bson.M{"_id": id, "owner_id": owner}, update)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (d *DB) deleteFileDocument(ctx context.Context, owner, id bson.ObjectID) (*fileDocument, error) {
	return d.deleteFile(ctx, bson.M{"_id": id, "owner_id": owner})
}

func (d *DB) deleteFileByKeyDocument(ctx context.Context, key string) (*fileDocument, error) {
	return d.deleteFile(ctx, bson.M{"object_key": key})
}

func (d *DB) deleteFile(ctx context.Context, filter bson.M) (*fileDocument, error) {
	var f fileDocument
	err := d.files.FindOneAndDelete(ctx, filter).Decode(&f)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &f, nil
}

func (d *DB) upsertAlertDocument(ctx context.Context, a *alertDocument) error {
	a.UpdatedAt = time.Now().UTC()
	_, err := d.alerts.UpdateOne(ctx,
		bson.M{"fingerprint": a.Fingerprint, "starts_at": a.StartsAt},
		bson.M{"$set": bson.M{
			"name": a.Name, "status": a.Status, "severity": a.Severity, "summary": a.Summary,
			"description": a.Description, "labels": a.Labels, "ends_at": a.EndsAt, "updated_at": a.UpdatedAt,
		}},
		options.UpdateOne().SetUpsert(true),
	)
	return err
}

func (d *DB) listAlertsDocument(ctx context.Context, limit int64) ([]alertDocument, error) {
	cur, err := d.alerts.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "updated_at", Value: -1}}).SetLimit(limit))
	if err != nil {
		return nil, err
	}
	alerts := []alertDocument{}
	return alerts, cur.All(ctx, &alerts)
}

func (d *DB) Stats(ctx context.Context) (domain.Stats, error) {
	var s domain.Stats
	cur, err := d.users.Aggregate(ctx, mongodriver.Pipeline{
		{{Key: "$group", Value: bson.M{
			"_id": nil, "users": bson.M{"$sum": 1},
			"used": bson.M{"$sum": "$used_bytes"}, "quota": bson.M{"$sum": "$quota_bytes"},
		}}},
	})
	if err != nil {
		return s, err
	}
	var rows []struct {
		Users int64 `bson:"users"`
		Used  int64 `bson:"used"`
		Quota int64 `bson:"quota"`
	}
	if err := cur.All(ctx, &rows); err != nil {
		return s, err
	}
	if len(rows) > 0 {
		s.Users, s.UsedBytes, s.QuotaBytes = rows[0].Users, rows[0].Used, rows[0].Quota
	}
	s.Files, err = d.files.EstimatedDocumentCount(ctx)
	return s, err
}

func findOne[T any](ctx context.Context, c *mongodriver.Collection, filter bson.M) (*T, error) {
	var v T
	err := c.FindOne(ctx, filter).Decode(&v)
	if errors.Is(err, mongodriver.ErrNoDocuments) {
		return nil, domain.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

func objectID(id string) (bson.ObjectID, error) {
	value, err := bson.ObjectIDFromHex(id)
	if err != nil {
		return value, domain.ErrNotFound
	}
	return value, nil
}
func userModel(d *userDocument) *domain.User {
	if d == nil {
		return nil
	}
	return &domain.User{ID: d.ID.Hex(), Email: d.Email, PasswordHash: d.PasswordHash, QuotaBytes: d.QuotaBytes, UsedBytes: d.UsedBytes, CreatedAt: d.CreatedAt}
}
func fileModel(d *fileDocument) *domain.File {
	if d == nil {
		return nil
	}
	return &domain.File{ID: d.ID.Hex(), OwnerID: d.OwnerID.Hex(), Name: d.Name, Size: d.Size, ContentType: d.ContentType, ObjectKey: d.ObjectKey, ShareToken: d.ShareToken, CreatedAt: d.CreatedAt}
}
func alertModel(d *alertDocument) domain.Alert {
	return domain.Alert{ID: d.ID.Hex(), Fingerprint: d.Fingerprint, Name: d.Name, Status: d.Status, Severity: d.Severity, Summary: d.Summary, Description: d.Description, Labels: d.Labels, StartsAt: d.StartsAt, EndsAt: d.EndsAt, UpdatedAt: d.UpdatedAt}
}

func (d *DB) CreateUser(ctx context.Context, user *domain.User) error {
	document := &userDocument{Email: user.Email, PasswordHash: user.PasswordHash, QuotaBytes: user.QuotaBytes, UsedBytes: user.UsedBytes}
	if err := d.createUserDocument(ctx, document); err != nil {
		return err
	}
	*user = *userModel(document)
	return nil
}
func (d *DB) UserByEmail(ctx context.Context, email string) (*domain.User, error) {
	doc, err := d.userByEmailDocument(ctx, email)
	return userModel(doc), err
}
func (d *DB) UserByID(ctx context.Context, id string) (*domain.User, error) {
	oid, err := objectID(id)
	if err != nil {
		return nil, err
	}
	doc, err := d.userByIDDocument(ctx, oid)
	return userModel(doc), err
}
func (d *DB) ReserveSpace(ctx context.Context, id string, size int64) error {
	oid, err := objectID(id)
	if err != nil {
		return err
	}
	return d.reserveSpaceDocument(ctx, oid, size)
}
func (d *DB) ReleaseSpace(ctx context.Context, id string, size int64) error {
	oid, err := objectID(id)
	if err != nil {
		return err
	}
	return d.releaseSpaceDocument(ctx, oid, size)
}
func (d *DB) CreateFile(ctx context.Context, file *domain.File) error {
	id, err := objectID(file.ID)
	if err != nil {
		return err
	}
	owner, err := objectID(file.OwnerID)
	if err != nil {
		return err
	}
	return d.createFileDocument(ctx, &fileDocument{ID: id, OwnerID: owner, Name: file.Name, Size: file.Size, ContentType: file.ContentType, ObjectKey: file.ObjectKey, ShareToken: file.ShareToken, CreatedAt: file.CreatedAt})
}
func (d *DB) ListFiles(ctx context.Context, owner, query string) ([]domain.File, error) {
	oid, err := objectID(owner)
	if err != nil {
		return nil, err
	}
	docs, err := d.listFilesDocument(ctx, oid, query)
	if err != nil {
		return nil, err
	}
	result := make([]domain.File, 0, len(docs))
	for i := range docs {
		result = append(result, *fileModel(&docs[i]))
	}
	return result, nil
}
func (d *DB) FileByID(ctx context.Context, owner, id string) (*domain.File, error) {
	oid, err := objectID(owner)
	if err != nil {
		return nil, err
	}
	fid, err := objectID(id)
	if err != nil {
		return nil, err
	}
	doc, err := d.fileByIDDocument(ctx, oid, fid)
	return fileModel(doc), err
}
func (d *DB) FileByShareToken(ctx context.Context, token string) (*domain.File, error) {
	doc, err := d.fileByShareTokenDocument(ctx, token)
	return fileModel(doc), err
}
func (d *DB) RenameFile(ctx context.Context, owner, id, name string) error {
	oid, err := objectID(owner)
	if err != nil {
		return err
	}
	fid, err := objectID(id)
	if err != nil {
		return err
	}
	return d.renameFileDocument(ctx, oid, fid, name)
}
func (d *DB) SetShareToken(ctx context.Context, owner, id, token string) error {
	oid, err := objectID(owner)
	if err != nil {
		return err
	}
	fid, err := objectID(id)
	if err != nil {
		return err
	}
	return d.setShareTokenDocument(ctx, oid, fid, token)
}
func (d *DB) DeleteFile(ctx context.Context, owner, id string) (*domain.File, error) {
	oid, err := objectID(owner)
	if err != nil {
		return nil, err
	}
	fid, err := objectID(id)
	if err != nil {
		return nil, err
	}
	doc, err := d.deleteFileDocument(ctx, oid, fid)
	return fileModel(doc), err
}
func (d *DB) DeleteFileByKey(ctx context.Context, key string) (*domain.File, error) {
	doc, err := d.deleteFileByKeyDocument(ctx, key)
	return fileModel(doc), err
}
func (d *DB) UpsertAlert(ctx context.Context, alert *domain.Alert) error {
	return d.upsertAlertDocument(ctx, &alertDocument{Fingerprint: alert.Fingerprint, Name: alert.Name, Status: alert.Status, Severity: alert.Severity, Summary: alert.Summary, Description: alert.Description, Labels: alert.Labels, StartsAt: alert.StartsAt, EndsAt: alert.EndsAt, UpdatedAt: alert.UpdatedAt})
}
func (d *DB) ListAlerts(ctx context.Context, limit int64) ([]domain.Alert, error) {
	docs, err := d.listAlertsDocument(ctx, limit)
	if err != nil {
		return nil, err
	}
	result := make([]domain.Alert, 0, len(docs))
	for i := range docs {
		result = append(result, alertModel(&docs[i]))
	}
	return result, nil
}
