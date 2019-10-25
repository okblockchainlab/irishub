package stake

import (
	"fmt"
	"sort"

	"github.com/irisnet/irishub/app/v1/stake/types"
	sdk "github.com/irisnet/irishub/types"
	abci "github.com/tendermint/tendermint/abci/types"
	tmtypes "github.com/tendermint/tendermint/types"
)

// InitGenesis sets the pool and parameters for the provided keeper.  For each
// validator in data, it sets that validator in the keeper along with manually
// setting the indexes. In addition, it also sets any delegations found in
// data. Finally, it updates the bonded validators.
func InitGenesis(ctx sdk.Context, keeper Keeper, data types.GenesisState) (res []abci.ValidatorUpdate, err error) {
	if err := ValidateGenesis(data); err != nil {
		panic(err.Error())
	}
	// We need to pretend to be "n blocks before genesis", where "n" is the validator update delay,
	// so that e.g. slashing periods are correctly initialized for the validator set
	// e.g. with a one-block offset - the first TM block is at height 0, so state updates applied from genesis.json are in block -1.
	ctx = ctx.WithBlockHeight(1 - types.ValidatorUpdateDelay)

	keeper.SetPool(ctx, types.Pool{BondedPool: data.BondedPool})
	keeper.SetParams(ctx, data.Params)
	keeper.SetLastTotalPower(ctx, data.LastTotalPower)

	pool := keeper.GetPool(ctx)
	for _, validator := range data.Validators {
		keeper.SetValidator(ctx, validator)

		// Manually set indices for the first time
		keeper.SetValidatorByConsAddr(ctx, validator)
		keeper.SetValidatorByPowerIndex(ctx, validator, pool)
		keeper.OnValidatorCreated(ctx, validator.OperatorAddr)

		// Set timeslice if necessary
		if validator.Status == sdk.Unbonding {
			keeper.InsertValidatorQueue(ctx, validator)
		}

		// Increase loosen token
		if validator.Status != sdk.Bonded {
			balance := sdk.NewCoin(types.StakeDenom, validator.Tokens.TruncateInt())
			pool.BankKeeper.IncreaseLoosenToken(ctx, sdk.Coins{balance})
		}
	}

	for _, delegation := range data.Bonds {
		keeper.SetDelegation(ctx, delegation)
		keeper.OnDelegationCreated(ctx, delegation.DelegatorAddr, delegation.ValidatorAddr)
	}

	sort.SliceStable(data.UnbondingDelegations[:], func(i, j int) bool {
		return data.UnbondingDelegations[i].CreationHeight < data.UnbondingDelegations[j].CreationHeight
	})
	for _, ubd := range data.UnbondingDelegations {
		keeper.SetUnbondingDelegation(ctx, ubd)
		keeper.InsertUnbondingQueue(ctx, ubd)
		pool.BankKeeper.IncreaseLoosenToken(ctx, sdk.Coins{ubd.Balance})
	}

	sort.SliceStable(data.Redelegations[:], func(i, j int) bool {
		return data.Redelegations[i].CreationHeight < data.Redelegations[j].CreationHeight
	})
	for _, red := range data.Redelegations {
		keeper.SetRedelegation(ctx, red)
		keeper.InsertRedelegationQueue(ctx, red)
	}

	// don't need to run Tendermint updates if we exported
	if data.Exported {
		for _, lv := range data.LastValidatorPowers {
			keeper.SetLastValidatorPower(ctx, lv.Address, lv.Power)
			validator, found := keeper.GetValidator(ctx, lv.Address)
			if !found {
				panic("expected validator, not found")
			}
			update := validator.ABCIValidatorUpdate()
			update.Power = lv.Power.Int64() // keep the next-val-set offset, use the last power for the first block
			res = append(res, update)
		}
	} else {
		res = keeper.ApplyAndReturnValidatorSetUpdates(ctx)
	}

	return
}

// ExportGenesis returns a GenesisState for a given context and keeper. The
// GenesisState will contain the pool, params, validators, and bonds found in
// the keeper.
func ExportGenesis(ctx sdk.Context, keeper Keeper) types.GenesisState {
	pool := keeper.GetPool(ctx).BondedPool
	params := keeper.GetParams(ctx)
	lastTotalPower := keeper.GetLastTotalPower(ctx)
	validators := keeper.GetAllValidators(ctx)
	bonds := keeper.GetAllDelegations(ctx)
	var unbondingDelegations []types.UnbondingDelegation
	keeper.IterateUnbondingDelegations(ctx, func(_ int64, ubd types.UnbondingDelegation) (stop bool) {
		unbondingDelegations = append(unbondingDelegations, ubd)
		return false
	})
	var redelegations []types.Redelegation
	keeper.IterateRedelegations(ctx, func(_ int64, red types.Redelegation) (stop bool) {
		redelegations = append(redelegations, red)
		return false
	})

	var lastValidatorPowers []types.LastValidatorPower
	keeper.IterateLastValidatorPowers(ctx, func(addr sdk.ValAddress, power sdk.Int) (stop bool) {
		lastValidatorPowers = append(lastValidatorPowers, types.LastValidatorPower{addr, power})
		return false
	})

	return types.GenesisState{
		BondedPool:           pool,
		Params:               params,
		LastTotalPower:       lastTotalPower,
		LastValidatorPowers:  lastValidatorPowers,
		Validators:           validators,
		Bonds:                bonds,
		UnbondingDelegations: unbondingDelegations,
		Redelegations:        redelegations,
		Exported:             true,
	}
}

// WriteValidators returns a slice of bonded genesis validators.
func WriteValidators(ctx sdk.Context, keeper Keeper) (vals []tmtypes.GenesisValidator) {
	keeper.IterateLastValidators(ctx, func(_ int64, validator sdk.Validator) (stop bool) {
		vals = append(vals, tmtypes.GenesisValidator{
			PubKey: validator.GetConsPubKey(),
			Power:  validator.GetPower().RoundInt64(),
			Name:   validator.GetMoniker(),
		})

		return false
	})

	return
}

// ValidateGenesis validates the provided staking genesis state to ensure the
// expected invariants holds. (i.e. params in correct bounds, no duplicate validators)
func ValidateGenesis(data types.GenesisState) error {
	err := validateGenesisStateValidators(data.Validators)
	if err != nil {
		return err
	}
	err = types.ValidateParams(data.Params)
	if err != nil {
		return err
	}

	return nil
}

func validateGenesisStateValidators(validators []types.Validator) (err error) {
	addrMap := make(map[string]bool, len(validators))
	for i := 0; i < len(validators); i++ {
		val := validators[i]
		strKey := string(val.ConsPubKey.Bytes())
		if _, ok := addrMap[strKey]; ok {
			return fmt.Errorf("duplicate validator in genesis state: moniker %v, Address %v", val.Description.Moniker, val.ConsAddress())
		}
		if val.Jailed && val.Status == sdk.Bonded {
			return fmt.Errorf("validator is bonded and jailed in genesis state: moniker %v, Address %v", val.Description.Moniker, val.ConsAddress())
		}
		if val.DelegatorShares.IsZero() && val.Status != sdk.Unbonding {
			return fmt.Errorf("bonded/unbonded genesis validator cannot have zero delegator shares, validator: %v", val)
		}
		addrMap[strKey] = true
	}
	return
}