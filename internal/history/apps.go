package history

import "context"

// AppIcon is the icon of a source app. Icon is nil until fetched, or when
// the app has none.
type AppIcon struct {
	App, Path string
	Icon      []byte
}

// SourceApp is an app entries were copied from.
type SourceApp struct{ App, Name string }

// AppIconsMissing lists apps whose icon hasn't been looked up yet.
func (s *Store) AppIconsMissing(ctx context.Context) ([]AppIcon, error) {
	rows, err := s.db.Query(ctx, `SELECT app,path FROM app_icons WHERE fetched=0`)
	if err != nil {
		return nil, err
	}
	out := make([]AppIcon, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, AppIcon{App: asString(r[0]), Path: asString(r[1])})
	}
	return out, nil
}

// SetAppIcon stores an app's icon PNG; nil records that it has none.
func (s *Store) SetAppIcon(ctx context.Context, app string, png []byte) error {
	_, err := s.db.Execute(ctx, `UPDATE app_icons SET icon=?, fetched=1 WHERE app=?`, nullable(png), app)
	return err
}

// AppIcons lists every fetched icon.
func (s *Store) AppIcons(ctx context.Context) ([]AppIcon, error) {
	rows, err := s.db.Query(ctx, `SELECT app,path,icon FROM app_icons WHERE fetched=1 AND icon IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	out := make([]AppIcon, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, AppIcon{App: asString(r[0]), Path: asString(r[1]), Icon: asBytes(r[2])})
	}
	return out, nil
}

// SourceApps lists the n apps most recently copied from.
func (s *Store) SourceApps(ctx context.Context, n int) ([]SourceApp, error) {
	rows, err := s.db.Query(ctx, `SELECT source_app, max(source_name) FROM entries WHERE source_app!='' GROUP BY source_app ORDER BY max(used_at) DESC LIMIT ?`, n)
	if err != nil {
		return nil, err
	}
	out := make([]SourceApp, 0, len(rows.Values))
	for _, r := range rows.Values {
		out = append(out, SourceApp{App: asString(r[0]), Name: asString(r[1])})
	}
	return out, nil
}
