package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
)

// MemoryService implements the MemoryService interface
type MemoryService struct {
	repo         interfaces.MemoryRepository
	modelService interfaces.ModelService
}

// NewMemoryService creates a new memory service
func NewMemoryService(repo interfaces.MemoryRepository, modelService interfaces.ModelService) interfaces.MemoryService {
	return &MemoryService{
		repo:         repo,
		modelService: modelService,
	}
}

// extractUserContextPrompt extracts user context from user messages ONLY.
//
// Design rationale (first-principles):
//   - LLM-generated answers are derived, not authoritative — writing them back to memory
//     creates a self-referential loop where hallucinations get stored, retrieved, and amplified.
//   - The KB already holds product/domain facts (graph.go handles that layer).
//   - Memory's job is to build a *user model*: who is this user, what they care about,
//     what decisions/constraints they carry across sessions.
//   - Only user messages are authoritative for the user model.
const extractUserContextPrompt = `You are building a user model from a conversation.
Your task: extract what the USER reveals about themselves — their role, use cases, decisions, stated conclusions, and constraints.

CRITICAL rules:
- Extract ONLY from user messages (provided below). Do NOT invent or infer facts not stated by the user.
- Do NOT extract product technical specs or facts — those belong in the knowledge base, not user memory.
- Entity types must be one of: UserRole / UseCase / Decision / Preference / Constraint / Topic
- If the user states a conclusion or decision, capture it verbatim in the description.
- If nothing user-specific can be extracted, return empty entities and relationships arrays.

Output JSON:
{
  "summary": "one sentence: user's intent and context in this session",
  "entities": [
    {
      "title": "concise entity name",
      "type": "UserRole|UseCase|Decision|Preference|Constraint|Topic",
      "description": "what the user revealed, using their own words where possible"
    }
  ],
  "relationships": [
    {
      "source": "entity title",
      "target": "entity title",
      "description": "how they relate from user's perspective",
      "weight": 1.0
    }
  ]
}

User messages:
%s
`

// conflictResolutionPrompt decides how to merge new entity info with existing memory.
//
// Implements the Mem0-style add/update/noop decision to prevent stale or
// contradictory information accumulating in the graph over time.
const conflictResolutionPrompt = `You are merging new user context into an existing user memory graph.

For each new entity, compare it against the existing entity (if any) and decide:
- "add":    entity does not exist yet — create it
- "update": entity exists but the new description adds meaningful new information — merge them
- "noop":   entity exists and new info is redundant or less specific — skip

Existing entities (from memory):
%s

New entities to evaluate:
%s

Output a JSON array — one entry per new entity in the same order:
[
  {
    "title": "entity title",
    "action": "add|update|noop",
    "merged_description": "final description to store (only relevant for add/update)"
  }
]
`

const extractKeywordsPrompt = `
You are an AI assistant that extracts search keywords from a user query.
Given the following query, extract relevant keywords for searching a knowledge graph.
Output the result in JSON format:
{
  "keywords": ["keyword1", "keyword2"]
}

Query:
%s
`

type extractionResult struct {
	Summary       string                `json:"summary"`
	Entities      []*types.Entity       `json:"entities"`
	Relationships []*types.Relationship `json:"relationships"`
}

type keywordsResult struct {
	Keywords []string `json:"keywords"`
}

// conflictDecision is the per-entity merge decision from the LLM.
type conflictDecision struct {
	Title              string `json:"title"`
	Action             string `json:"action"` // "add" | "update" | "noop"
	MergedDescription  string `json:"merged_description"`
}

func (s *MemoryService) getChatModel(ctx context.Context) (chat.Chat, error) {
	models, err := s.modelService.ListModels(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list models: %v", err)
	}

	var modelID string
	for _, model := range models {
		if model.Type == types.ModelTypeKnowledgeQA {
			modelID = model.ID
			break
		}
	}
	if modelID == "" {
		return nil, fmt.Errorf("no KnowledgeQA model found")
	}
	return s.modelService.GetChatModel(ctx, modelID)
}

// AddEpisode extracts user context from user messages and merges it into the memory graph.
//
// Key invariant: only Role=="user" messages are processed. Assistant-generated answers are
// excluded because they are LLM outputs and may contain hallucinations; writing them back
// would corrupt the memory graph and amplify errors across sessions.
func (s *MemoryService) AddEpisode(ctx context.Context, userID string, sessionID string, messages []types.Message) error {
	if !s.repo.IsAvailable(ctx) {
		return fmt.Errorf("memory repository is not available")
	}
	chatModel, err := s.getChatModel(ctx)
	if err != nil {
		return err
	}

	// 1. Collect user messages only — assistant answers are derived, not authoritative.
	var userLines []string
	for _, msg := range messages {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) != "" {
			userLines = append(userLines, msg.Content)
		}
	}
	if len(userLines) == 0 {
		// Nothing user-specific to extract; skip silently.
		return nil
	}
	userText := strings.Join(userLines, "\n")

	// 2. Extract user context entities from user messages.
	extractPrompt := fmt.Sprintf(extractUserContextPrompt, userText)
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: extractPrompt}}, &chat.ChatOptions{
		Format: utils.GenerateSchema[extractionResult](),
	})
	if err != nil {
		return fmt.Errorf("failed to extract user context: %v", err)
	}

	var extracted extractionResult
	if err := json.Unmarshal([]byte(resp.Content), &extracted); err != nil {
		return fmt.Errorf("failed to parse extraction result: %v", err)
	}
	if len(extracted.Entities) == 0 {
		// Nothing meaningful found; still record the episode summary.
		episode := &types.Episode{
			ID:        uuid.New().String(),
			UserID:    userID,
			SessionID: sessionID,
			Summary:   extracted.Summary,
			CreatedAt: time.Now(),
		}
		return s.repo.SaveEpisode(ctx, episode, nil, nil)
	}

	// 3. Conflict detection: fetch existing entities with same names (user-scoped).
	newNames := make([]string, 0, len(extracted.Entities))
	for _, e := range extracted.Entities {
		newNames = append(newNames, e.Title)
	}

	existingEntities, err := s.repo.FindEntitiesByNames(ctx, userID, newNames)
	if err != nil {
		// Non-fatal: if conflict check fails, fall through to save everything as new.
		existingEntities = nil
	}

	// 4. Run merge decision only when there are conflicts to resolve.
	finalEntities := extracted.Entities
	if len(existingEntities) > 0 {
		finalEntities, err = s.resolveConflicts(ctx, chatModel, extracted.Entities, existingEntities)
		if err != nil {
			// Non-fatal: fall back to saving all new entities unchanged.
			finalEntities = extracted.Entities
		}
	}

	// 5. Save episode with resolved entities.
	episode := &types.Episode{
		ID:        uuid.New().String(),
		UserID:    userID,
		SessionID: sessionID,
		Summary:   extracted.Summary,
		CreatedAt: time.Now(),
	}
	return s.repo.SaveEpisode(ctx, episode, finalEntities, extracted.Relationships)
}

// resolveConflicts asks the LLM to decide add/update/noop for each new entity
// against the existing entities already stored in the user's memory graph.
func (s *MemoryService) resolveConflicts(
	ctx context.Context,
	chatModel chat.Chat,
	newEntities []*types.Entity,
	existingEntities []*types.Entity,
) ([]*types.Entity, error) {
	// Serialise both lists for the prompt.
	existingJSON, err := json.Marshal(existingEntities)
	if err != nil {
		return newEntities, err
	}
	newJSON, err := json.Marshal(newEntities)
	if err != nil {
		return newEntities, err
	}

	prompt := fmt.Sprintf(conflictResolutionPrompt, string(existingJSON), string(newJSON))
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{
		Format: utils.GenerateSchema[[]conflictDecision](),
	})
	if err != nil {
		return newEntities, fmt.Errorf("conflict resolution LLM call failed: %v", err)
	}

	var decisions []conflictDecision
	if err := json.Unmarshal([]byte(resp.Content), &decisions); err != nil {
		return newEntities, fmt.Errorf("failed to parse conflict decisions: %v", err)
	}

	// Build a lookup so we can apply decisions by title.
	decisionMap := make(map[string]conflictDecision, len(decisions))
	for _, d := range decisions {
		decisionMap[d.Title] = d
	}

	var result []*types.Entity
	for _, entity := range newEntities {
		d, ok := decisionMap[entity.Title]
		if !ok {
			// No decision returned — treat as add.
			result = append(result, entity)
			continue
		}
		switch d.Action {
		case "noop":
			// Skip: existing memory is already accurate or more specific.
			continue
		case "update":
			entity.Description = d.MergedDescription
			result = append(result, entity)
		default: // "add" or unknown
			result = append(result, entity)
		}
	}
	return result, nil
}

// RetrieveMemory retrieves relevant memory context based on the current query and user.
func (s *MemoryService) RetrieveMemory(ctx context.Context, userID string, query string) (*types.MemoryContext, error) {
	if !s.repo.IsAvailable(ctx) {
		return nil, fmt.Errorf("memory repository is not available")
	}
	chatModel, err := s.getChatModel(ctx)
	if err != nil {
		return nil, err
	}

	// 1. Extract keywords from query.
	prompt := fmt.Sprintf(extractKeywordsPrompt, query)
	resp, err := chatModel.Chat(ctx, []chat.Message{{Role: "user", Content: prompt}}, &chat.ChatOptions{
		Format: utils.GenerateSchema[keywordsResult](),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to call LLM: %v", err)
	}

	var result keywordsResult
	if err := json.Unmarshal([]byte(resp.Content), &result); err != nil {
		return nil, fmt.Errorf("failed to parse LLM response: %v", err)
	}

	// 2. Retrieve related episodes.
	episodes, err := s.repo.FindRelatedEpisodes(ctx, userID, result.Keywords, 5)
	if err != nil {
		return nil, fmt.Errorf("failed to find related episodes: %v", err)
	}

	// 3. Construct MemoryContext.
	memoryContext := &types.MemoryContext{
		RelatedEpisodes: make([]types.Episode, len(episodes)),
	}
	for i, ep := range episodes {
		memoryContext.RelatedEpisodes[i] = *ep
	}
	return memoryContext, nil
}
