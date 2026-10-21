package policy

import (
	"errors"
	"net/url"
	"strings"
	"whirlwind/internal/model"
)

func validateLessonEmbeddings(config *model.Embeddings) error {
	if config == nil || !config.LessonsEnabled {
		return nil
	}
	target, err := url.ParseRequestURI(config.Endpoint)
	if err != nil {
		return errors.New("embeddings.lessons_enabled requires an explicit HTTP(S) endpoint")
	}
	if target.Hostname() == "" || target.Scheme != "http" && target.Scheme != "https" {
		return errors.New("embeddings.lessons_enabled requires an explicit HTTP(S) endpoint")
	}
	if strings.TrimSpace(config.Model) == "" {
		return errors.New("embeddings.lessons_enabled requires an explicit model")
	}
	return nil
}
