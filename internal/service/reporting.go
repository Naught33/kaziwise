// Dashboard aggregation for the manager screens. The store exposes the
// pieces as separate queries (KPIs, attention, department progress); the
// staff dashboard is one screen, so the service assembles the parts into a
// single payload rather than making the browser fan out four requests.
package service

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/kaziwise/kaziwise_backend/internal/domain"
	"github.com/kaziwise/kaziwise_backend/internal/store"
)

// Row limits for the dashboard panels. Each panel shows a summary, so a
// bounded list is enough and keeps the payload small.
const (
	dashboardCampaigns    = 10
	dashboardCertificates = 8
)

// AdminDashboard assembles the staff-facing dashboard (screen 01). The
// learner counterpart is LearnerDashboard; this one is scoped to the whole
// organisation, so it is only ever called behind a manage-role check.
func (s *Service) AdminDashboard(ctx context.Context, orgID uuid.UUID) (*domain.Dashboard, error) {
	kpis, err := s.DB.DashboardKPIs(ctx, orgID)
	if err != nil {
		return nil, err
	}

	// Only running campaigns: the panel's columns are assigned count,
	// progress and due date, none of which mean anything for a draft.
	campaigns, _, err := s.DB.ListCampaigns(ctx, orgID, store.CampaignListFilter{
		Status:  string(domain.CampaignActive),
		Page:    1,
		PerPage: dashboardCampaigns,
	})
	if err != nil {
		return nil, err
	}

	attention, err := s.DB.AttentionItems(ctx, orgID, 20)
	if err != nil {
		return nil, err
	}

	departments, err := s.DB.DepartmentProgress(ctx, orgID, nil)
	if err != nil {
		return nil, err
	}

	// A nil learner id means every certificate in the organisation.
	certificates, _, err := s.DB.ListCertificates(ctx, orgID, nil, 1, dashboardCertificates)
	if err != nil {
		return nil, err
	}

	// The JSON contract declares arrays, so an empty result must encode as
	// [] rather than null or the front end reads .length on undefined.
	if campaigns == nil {
		campaigns = []domain.Campaign{}
	}
	if attention == nil {
		attention = []domain.AttentionItem{}
	}
	if departments == nil {
		departments = []domain.DepartmentProgress{}
	}
	if certificates == nil {
		certificates = []domain.Certificate{}
	}

	return &domain.Dashboard{
		KPIs:               *kpis,
		ActiveCampaigns:    campaigns,
		Attention:          attention,
		DepartmentProgress: departments,
		RecentCertificates: certificates,
		GeneratedAt:        time.Now(),
	}, nil
}
