package logx

import (
	"context"
	"io"
	"log/slog"
)

type byKey struct{}

// WithBy records the account a request came from; events logged with that
// context carry it as "by".
func WithBy(ctx context.Context, account string) context.Context {
	return context.WithValue(ctx, byKey{}, account)
}

func By(ctx context.Context) string {
	s, _ := ctx.Value(byKey{}).(string)
	return s
}

// JSON logs JSON lines to w, with "by" from the context when set.
func JSON(w io.Writer) *slog.Logger { return slog.New(byHandler{slog.NewJSONHandler(w, nil)}) }

type byHandler struct{ slog.Handler }

func (h byHandler) Handle(ctx context.Context, r slog.Record) error {
	if by := By(ctx); by != "" {
		r = r.Clone()
		r.AddAttrs(slog.String("by", by))
	}
	return h.Handler.Handle(ctx, r)
}

func (h byHandler) WithAttrs(as []slog.Attr) slog.Handler { return byHandler{h.Handler.WithAttrs(as)} }
func (h byHandler) WithGroup(name string) slog.Handler    { return byHandler{h.Handler.WithGroup(name)} }
