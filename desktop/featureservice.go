package main

import (
	"github.com/mrgeoffrich/bacio/internal/model"
	"github.com/mrgeoffrich/bacio/internal/store"
)

type FeatureService struct {
	store *store.Store
}

func NewFeatureService(s *store.Store) *FeatureService {
	return &FeatureService{store: s}
}

func (f *FeatureService) List() ([]*model.Feature, error) {
	return f.store.ListAllFeatures(true)
}
