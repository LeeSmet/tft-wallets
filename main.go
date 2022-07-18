package main

import (
	"fmt"
	"strconv"

	"github.com/pkg/errors"
	"github.com/stellar/go/clients/horizonclient"
	"github.com/stellar/go/protocols/horizon"
)

const escrowSigner = "preauth_tx"
const tftasset = "TFT:GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47"
const tftaasset = "TFTA:GBUT4GP5GJ6B3XW5PXENHQA7TXJI5GOPW3NF4W3ZIW6OOO4ISY6WNLN2"

const vestingDataKey = "tft-vesting"

func main() {
	client := horizonclient.DefaultPublicNetClient

	tftAccounts := []horizon.Account{}
	tftaAccounts := []horizon.Account{}
	cursor := ""
	for {
		accreq := horizonclient.AccountsRequest{
			Asset:  tftasset,
			Limit:  50,
			Cursor: cursor,
		}
		accs, err := client.Accounts(accreq)
		if err != nil {
			e := err.(*horizonclient.Error)
			fmt.Println(e.Problem)
			panic(e)
		}

		tftAccounts = append(tftAccounts, accs.Embedded.Records...)

		cursor = accs.Embedded.Records[len(accs.Embedded.Records)-1].PagingToken()
		if len(accs.Embedded.Records) < 50 {
			break
		}
	}

	cursor = ""
	for {
		accreq := horizonclient.AccountsRequest{
			Asset:  tftaasset,
			Limit:  50,
			Cursor: cursor,
		}
		accs, err := client.Accounts(accreq)
		if err != nil {
			e := err.(*horizonclient.Error)
			fmt.Println(e.Problem)
			panic(e)
		}

		tftaAccounts = append(tftaAccounts, accs.Embedded.Records...)

		cursor = accs.Embedded.Records[len(accs.Embedded.Records)-1].PagingToken()
		if len(accs.Embedded.Records) < 50 {
			break
		}
	}

	// dedup accounts
	seen := make(map[string]struct{})
	accounts := []horizon.Account{}
	for _, acc := range append(tftAccounts, tftaAccounts...) {
		if _, exists := seen[acc.AccountID]; exists {
			continue
		}
		accounts = append(accounts, acc)
		seen[acc.AccountID] = struct{}{}
	}

	// escrow source to target
	escrows := make(map[string]string)
	// locked tft and tfta for a given account
	lockedtft := make(map[string]float64)
	lockedtfta := make(map[string]float64)

	isEscrow := func(acc horizon.Account) (string, bool) {
		if len(acc.Signers) != 3 {
			return "", false
		}

		sigs := []horizon.Signer{}
		for _, s := range acc.Signers {
			if s.Type != escrowSigner {
				sigs = append(sigs, s)
			}
		}

		if sigs[0].Key == acc.AccountID {
			for _, t := range accounts {
				if t.AccountID != acc.AccountID && sigs[1].Key == t.AccountID {
					return t.AccountID, true
				}
			}
		}
		if sigs[1].Key == acc.AccountID {
			for _, t := range accounts {
				if t.AccountID != acc.AccountID && sigs[0].Key == t.AccountID {
					return t.AccountID, true
				}
			}
		}

		return "", false
	}

	// map out escrows and locked funds
	for _, acc := range accounts {
		if target, ie := isEscrow(acc); ie {
			escrows[acc.AccountID] = target
			for _, balance := range acc.Balances {
				if balance.Asset.Issuer == "GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47" && balance.Asset.Code == "TFT" {
					bal, err := strconv.ParseFloat(balance.Balance, 64)
					if err != nil {
						panic(err)
					}
					lockedtft[target] = lockedtft[target] + bal
				}
				if balance.Asset.Issuer == "GBUT4GP5GJ6B3XW5PXENHQA7TXJI5GOPW3NF4W3ZIW6OOO4ISY6WNLN2" && balance.Asset.Code == "TFTA" {
					bal, err := strconv.ParseFloat(balance.Balance, 64)
					if err != nil {
						panic(err)
					}
					lockedtfta[target] = lockedtfta[target] + bal
				}
			}
		}
	}

	// map vested stuffs
	vestingAcc := make(map[string]string)      // vested account -> owner
	vestingBalance := make(map[string]float64) // vesting owner -> balance
	for _, acc := range accounts {
		target, iv, err := isVestingAccount(acc)
		if err != nil {
			panic(fmt.Sprintf("error determining if account is vesting acc: %s", err))
		}
		if !iv {
			continue
		}
		vestingAcc[acc.AccountID] = target
		for _, balance := range acc.Balances {
			if balance.Asset.Issuer == "GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47" && balance.Asset.Code == "TFT" {
				bal, err := strconv.ParseFloat(balance.Balance, 64)
				if err != nil {
					panic(err)
				}
				vestingBalance[target] = vestingBalance[target] + bal
			}
		}
	}

	// construct csv
	fmt.Println("Account,TFT Unlocked,TFT Locked,TFTA Unlocked,TFTA Locked,Vested,Name")

	for _, acc := range accounts {
		var err error
		var tft, ltft, tfta, ltfta, vested float64
		for _, balance := range acc.Balances {
			if balance.Asset.Issuer == "GBOVQKJYHXRR3DX6NOX2RRYFRCUMSADGDESTDNBDS6CDVLGVESRTAC47" && balance.Asset.Code == "TFT" {
				tft, err = strconv.ParseFloat(balance.Balance, 64)
				if err != nil {
					panic(err)
				}
			}
			if balance.Asset.Issuer == "GBUT4GP5GJ6B3XW5PXENHQA7TXJI5GOPW3NF4W3ZIW6OOO4ISY6WNLN2" && balance.Asset.Code == "TFTA" {
				tfta, err = strconv.ParseFloat(balance.Balance, 64)
				if err != nil {
					panic(err)
				}
			}
		}
		ltft = lockedtft[acc.AccountID]
		ltfta = lockedtfta[acc.AccountID]
		vested = vestingBalance[acc.AccountID]
		note := ""
		if target, exists := escrows[acc.AccountID]; exists {
			note = fmt.Sprintf("Escrow account for %s", target)
		} else if target, exists := vestingAcc[acc.AccountID]; exists {
			note = fmt.Sprintf("Vesting account for %s", target)
		}

		fmt.Printf("%s,%.7f,%.7f,%.7f,%.7f,%.7f,%s\n", acc.AccountID, tft, ltft, tfta, ltfta, vested, note)
	}
}

func isVestingSigner(address string) bool {
	var vestingSingers []string = []string{
		"GALQ4TZA6VRBBBBYMM3KSBSXJDLC5A7YIGH4SAS6AJ7N4ZA6P6IHWH43",
		"GARF35OFGW2XFHFG764UVO2UTUOSDRVL5DU7RXMM7JJJOSVWKK7GATXU",
		"GCHUIUY5MOBWOXEKZJEQU2DCUG4WHRXM4KAWCEUQK3NTQGBK5RZ6FQBR",
		"GDMMVCANURBLP6O64QWJM3L2EZTDSGTFL4B2BNXKAQPWYDX6WNAFNWK4",
		"GDORF4CKQ2GDOBXXU7R3EXV3XRN6LFCGNYTHMYXDPZ5NECZ6YZLJGAA2",
		"GDOSJPACWZ2DWSDNNKCVIKMUL3BNVVV3IERJPAZXM3PJMDNXYJIZFUL3",
		"GDSKTNDAIBUBGQZXEJ64F3P37T7Y45ZOZQCRZY2I46F4UT66KG4JJSOU",
		"GDTFYNE5MKGFL625FNUQUHILILFNNRSRYAAXADFFLMOOF5E6V5FLLSBG",
		"GDTTKKRECHQMYWJWKQ5UTONRMNK54WRN3PB4U7JZAPUHLPI75ALN7ORU",
	}

	for i := range vestingSingers {
		if address == vestingSingers[i] {
			return true
		}
	}

	return false
}

func isVestingAccount(acc horizon.Account) (string, bool, error) {
	data, err := acc.GetData(vestingDataKey)
	if err != nil {
		return "", false, errors.Wrap(err, "could not get account data")
	}

	if len(data) == 0 {
		return "", false, nil
	}

	for _, s := range acc.Signers {
		if s.Type != "ed25519_public_key" {
			continue
		}
		if s.Key == acc.AccountID {
			continue
		}
		if isVestingSigner(s.Key) {
			continue
		}
		if s.Weight != 5 {
			continue
		}
		return s.Key, true, nil
	}

	return "", false, nil
}
