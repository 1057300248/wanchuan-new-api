package extension

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMigrationPhasesAreClosedAndOrdered(t *testing.T) {
	assert.Equal(t, []MigrationPhase{PhaseAfterCoreModels, PhaseAfterCoreData}, MigrationPhases())
	assert.Equal(t, "after_core_models", PhaseAfterCoreModels.String())
	assert.Equal(t, "after_core_data", PhaseAfterCoreData.String())
	assert.Equal(t, "MigrationPhase(0)", MigrationPhase(0).String())

	assert.True(t, PhaseAfterCoreModels.valid())
	assert.True(t, PhaseAfterCoreData.valid())
	for _, phase := range []MigrationPhase{0, -1, 3, 99} {
		assert.False(t, phase.valid(), "phase %d must be rejected", int(phase))
	}

	// MigrationPhases hands out a copy of the canonical order.
	phases := MigrationPhases()
	phases[0] = PhaseAfterCoreData
	assert.Equal(t, PhaseAfterCoreModels, MigrationPhases()[0])
}

func TestRegisterModelSetRejectsInvalidDescriptors(t *testing.T) {
	var typedNilModel *modelA
	cases := []struct {
		name    string
		set     ModelSet
		wantErr string
	}{
		{
			name:    "empty extension",
			set:     ModelSet{Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}}},
			wantErr: "extension: model set <empty>/core has an empty extension",
		},
		{
			name:    "empty name",
			set:     ModelSet{Extension: "chuanapi", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}}},
			wantErr: "extension: model set chuanapi/<empty> has an empty name",
		},
		{
			name:    "invalid phase",
			set:     ModelSet{Extension: "chuanapi", Name: "core", Models: []any{&modelA{}}},
			wantErr: "extension: model set chuanapi/core has invalid phase MigrationPhase(0)",
		},
		{
			name:    "no models",
			set:     ModelSet{Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels},
			wantErr: "extension: model set chuanapi/core has no models",
		},
		{
			name: "nil model element",
			set: ModelSet{
				Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels,
				Models: []any{&modelA{}, nil},
			},
			wantErr: "extension: model set chuanapi/core has a nil model at index 1",
		},
		{
			name: "typed nil model element",
			set: ModelSet{
				Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels,
				Models: []any{typedNilModel},
			},
			wantErr: "extension: model set chuanapi/core has a nil model at index 0",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.RegisterModelSet(tc.set)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
			assert.Empty(t, reg.ModelSets(), "a rejected descriptor is not stored")
		})
	}
}

func TestRegisterDataStepRejectsInvalidDescriptors(t *testing.T) {
	cases := []struct {
		name    string
		step    DataStep
		wantErr string
	}{
		{
			name:    "empty extension",
			step:    DataStep{Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep},
			wantErr: "extension: data step <empty>/backfill has an empty extension",
		},
		{
			name:    "empty name",
			step:    DataStep{Extension: "chuanapi", Phase: PhaseAfterCoreData, Run: noopStep},
			wantErr: "extension: data step chuanapi/<empty> has an empty name",
		},
		{
			name:    "invalid phase",
			step:    DataStep{Extension: "chuanapi", Name: "backfill", Phase: MigrationPhase(7), Run: noopStep},
			wantErr: "extension: data step chuanapi/backfill has invalid phase MigrationPhase(7)",
		},
		{
			name:    "nil run",
			step:    DataStep{Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData},
			wantErr: "extension: data step chuanapi/backfill has a nil Run function",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := NewRegistry()
			err := reg.RegisterDataStep(tc.step)
			require.Error(t, err)
			assert.EqualError(t, err, tc.wantErr)
			assert.Empty(t, reg.Steps(), "a rejected descriptor is not stored")
		})
	}
}

func TestDuplicateMigrationIdentityIsRejectedWithoutOverwriting(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))
	err := reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreData, Models: []any{&modelB{}},
	})
	require.Error(t, err)
	assert.EqualError(t, err, "extension: duplicate model set chuanapi/core")

	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	err = reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreModels, Run: noopStep,
	})
	require.Error(t, err)
	assert.EqualError(t, err, "extension: duplicate data step chuanapi/backfill")

	// The originals survived: neither phase nor models were overwritten.
	sets := reg.ModelSets()
	require.Len(t, sets, 1)
	assert.Equal(t, PhaseAfterCoreModels, sets[0].Phase)
	assert.Equal(t, []any{&modelA{}}, sets[0].Models)
	steps := reg.Steps()
	require.Len(t, steps, 1)
	assert.Equal(t, PhaseAfterCoreData, steps[0].Phase)

	// A different name under the same extension is fine.
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "audit", Phase: PhaseAfterCoreModels, Models: []any{&modelB{}},
	}))
}

func TestMigrationOrderIsStableAcrossAndWithinPhases(t *testing.T) {
	reg := NewRegistry()
	// Register in an order that does not match phase order, so the accessors
	// have to sort by phase while preserving registration order inside a phase.
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "late-b", Phase: PhaseAfterCoreData, Models: []any{&modelB{}},
	}))
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "early-a", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}, &modelB{}},
	}))
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "late-a", Phase: PhaseAfterCoreData, Models: []any{&modelA{}},
	}))
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "early-b", Phase: PhaseAfterCoreModels, Models: []any{&modelB{}},
	}))
	require.NoError(t, reg.Freeze())

	var names []string
	for _, set := range reg.ModelSets() {
		names = append(names, set.Name)
	}
	assert.Equal(t, []string{"early-a", "early-b", "late-b", "late-a"}, names)

	// ModelsFor flattens in registration order and preserves within-set order.
	assert.Equal(t, []any{&modelA{}, &modelB{}, &modelB{}}, reg.ModelsFor(PhaseAfterCoreModels))
	assert.Equal(t, []any{&modelB{}, &modelA{}}, reg.ModelsFor(PhaseAfterCoreData))
	assert.Nil(t, reg.ModelsFor(MigrationPhase(0)), "unknown phase yields no models")

	// Repeated reads are identical.
	assert.Equal(t, reg.ModelsFor(PhaseAfterCoreModels), reg.ModelsFor(PhaseAfterCoreModels))
}

func TestStepOrderIsStableAcrossAndWithinPhases(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "data-1", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "models-1", Phase: PhaseAfterCoreModels, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "data-2", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "models-2", Phase: PhaseAfterCoreModels, Run: noopStep,
	}))
	require.NoError(t, reg.Freeze())

	var all []string
	for _, step := range reg.Steps() {
		all = append(all, step.Name)
	}
	assert.Equal(t, []string{"models-1", "models-2", "data-1", "data-2"}, all)

	var afterData []string
	for _, step := range reg.StepsFor(PhaseAfterCoreData) {
		afterData = append(afterData, step.Name)
	}
	assert.Equal(t, []string{"data-1", "data-2"}, afterData)
	assert.Nil(t, reg.StepsFor(MigrationPhase(9)), "unknown phase yields no steps")
}

func TestMigrationReadsReturnDefensiveCopies(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: []any{&modelA{}},
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.Freeze())

	sets := reg.ModelSets()
	sets[0].Name = "mutated"
	sets[0].Phase = PhaseAfterCoreData
	sets[0].Models[0] = &modelB{}
	require.Len(t, reg.ModelSets(), 1)
	assert.Equal(t, "core", reg.ModelSets()[0].Name)
	assert.Equal(t, PhaseAfterCoreModels, reg.ModelSets()[0].Phase)
	assert.Equal(t, []any{&modelA{}}, reg.ModelSets()[0].Models)

	models := reg.ModelsFor(PhaseAfterCoreModels)
	models[0] = &modelB{}
	assert.Equal(t, []any{&modelA{}}, reg.ModelsFor(PhaseAfterCoreModels))

	steps := reg.Steps()
	steps[0].Name = "mutated"
	assert.Equal(t, "backfill", reg.Steps()[0].Name)

	phaseSteps := reg.StepsFor(PhaseAfterCoreData)
	phaseSteps[0].Name = "mutated"
	assert.Equal(t, "backfill", reg.StepsFor(PhaseAfterCoreData)[0].Name)
}

func TestRegisterModelSetCopiesCallerSlice(t *testing.T) {
	reg := NewRegistry()
	models := []any{&modelA{}}
	require.NoError(t, reg.RegisterModelSet(ModelSet{
		Extension: "chuanapi", Name: "core", Phase: PhaseAfterCoreModels, Models: models,
	}))

	// Mutating the caller's slice after registration must not reach the registry.
	models[0] = &modelB{}
	assert.Equal(t, []any{&modelA{}}, reg.ModelsFor(PhaseAfterCoreModels))
}

func TestRunStepsForExecutesInOrderAndWrapsFailures(t *testing.T) {
	var executed []string
	reg := NewRegistry()
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "first", Phase: PhaseAfterCoreData,
		Run: func(*gorm.DB) error { executed = append(executed, "first"); return nil },
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "second", Phase: PhaseAfterCoreData,
		Run: func(*gorm.DB) error { executed = append(executed, "second"); return errBoom },
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "third", Phase: PhaseAfterCoreData,
		Run: func(*gorm.DB) error { executed = append(executed, "third"); return nil },
	}))
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "other-phase", Phase: PhaseAfterCoreModels,
		Run: func(*gorm.DB) error { executed = append(executed, "other-phase"); return nil },
	}))
	require.NoError(t, reg.Freeze())

	db := &gorm.DB{}
	err := reg.RunStepsFor(db, PhaseAfterCoreData)
	require.Error(t, err)
	assert.EqualError(t, err, "migration step chuanapi/second failed: boom")
	assert.ErrorIs(t, err, errBoom, "the cause stays inspectable")
	assert.Equal(t, []string{"first", "second"}, executed, "fail-closed: later steps do not run")
}

func TestRunStepsForReceivesTheGivenDatabase(t *testing.T) {
	db := &gorm.DB{}
	var got *gorm.DB
	reg := NewRegistry()
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "capture", Phase: PhaseAfterCoreModels,
		Run: func(tx *gorm.DB) error { got = tx; return nil },
	}))
	require.NoError(t, reg.Freeze())

	require.NoError(t, reg.RunStepsFor(db, PhaseAfterCoreModels))
	assert.Same(t, db, got)
	require.NoError(t, reg.RunStepsFor(db, PhaseAfterCoreData), "a phase with no steps is a no-op")
}

func TestRunStepsForRejectsInvalidPhaseAndNilDatabase(t *testing.T) {
	reg := NewRegistry()
	require.NoError(t, reg.RegisterDataStep(DataStep{
		Extension: "chuanapi", Name: "backfill", Phase: PhaseAfterCoreData, Run: noopStep,
	}))
	require.NoError(t, reg.Freeze())

	err := reg.RunStepsFor(&gorm.DB{}, MigrationPhase(0))
	require.Error(t, err)
	assert.EqualError(t, err, "extension: cannot run migration steps for invalid phase MigrationPhase(0)")

	err = reg.RunStepsFor(nil, PhaseAfterCoreData)
	require.Error(t, err)
	assert.EqualError(t, err, "extension: cannot run migration steps with a nil database")

	// With no steps for the phase there is nothing to run, so a nil database is
	// harmless rather than an error.
	require.NoError(t, reg.RunStepsFor(nil, PhaseAfterCoreModels))
}
