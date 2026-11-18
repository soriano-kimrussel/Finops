package quickbooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Wire formats follow the QuickBooks Online Accounting API (JSON, minorversion 75).
// Monetary fields decode into decimal.Decimal straight from the JSON number text.

type wireRef struct {
	Value string `json:"value"`
	Name  string `json:"name"`
}

type wireMeta struct {
	LastUpdatedTime string `json:"LastUpdatedTime"`
}

type wireCustomer struct {
	ID               string `json:"Id"`
	DisplayName      string `json:"DisplayName"`
	CompanyName      string `json:"CompanyName"`
	Active           *bool  `json:"Active"`
	PrimaryEmailAddr *struct {
		Address string `json:"Address"`
	} `json:"PrimaryEmailAddr"`
	PrimaryPhone *struct {
		FreeFormNumber string `json:"FreeFormNumber"`
	} `json:"PrimaryPhone"`
	BillAddr *struct {
		Line1                  string `json:"Line1"`
		City                   string `json:"City"`
		CountrySubDivisionCode string `json:"CountrySubDivisionCode"`
		PostalCode             string `json:"PostalCode"`
		Country                string `json:"Country"`
	} `json:"BillAddr"`
	CurrencyRef *wireRef  `json:"CurrencyRef"`
	MetaData    *wireMeta `json:"MetaData"`
}

type wireInvoice struct {
	ID           string          `json:"Id"`
	DocNumber    string          `json:"DocNumber"`
	TxnDate      string          `json:"TxnDate"`
	DueDate      string          `json:"DueDate"`
	TotalAmt     decimal.Decimal `json:"TotalAmt"`
	Balance      decimal.Decimal `json:"Balance"`
	CurrencyRef  *wireRef        `json:"CurrencyRef"`
	CustomerRef  wireRef         `json:"CustomerRef"`
	TxnTaxDetail *struct {
		TotalTax decimal.Decimal `json:"TotalTax"`
	} `json:"TxnTaxDetail"`
	Line []struct {
		Amount              decimal.Decimal `json:"Amount"`
		Description         string          `json:"Description"`
		DetailType          string          `json:"DetailType"`
		SalesItemLineDetail *struct {
			Qty       *decimal.Decimal `json:"Qty"`
			UnitPrice *decimal.Decimal `json:"UnitPrice"`
		} `json:"SalesItemLineDetail"`
	} `json:"Line"`
	MetaData *wireMeta `json:"MetaData"`
}

type wirePayment struct {
	ID          string          `json:"Id"`
	TxnDate     string          `json:"TxnDate"`
	TotalAmt    decimal.Decimal `json:"TotalAmt"`
	CurrencyRef *wireRef        `json:"CurrencyRef"`
	CustomerRef wireRef         `json:"CustomerRef"`
	Line        []struct {
		Amount    decimal.Decimal `json:"Amount"`
		LinkedTxn []struct {
			TxnID   string `json:"TxnId"`
			TxnType string `json:"TxnType"`
		} `json:"LinkedTxn"`
	} `json:"Line"`
	MetaData *wireMeta `json:"MetaData"`
}

func parseDate(s string) (time.Time, error) { return time.Parse("2006-01-02", strings.TrimSpace(s)) }

func parseMeta(m *wireMeta) *time.Time {
	if m == nil || m.LastUpdatedTime == "" {
		return nil
	}
	t, err := time.Parse(time.RFC3339, m.LastUpdatedTime)
	if err != nil {
		return nil
	}
	return &t
}

func currencyOf(ref *wireRef, fallback string) string {
	if ref != nil && ref.Value != "" {
		return strings.ToUpper(ref.Value)
	}
	return fallback
}

func decodeCustomer(raw json.RawMessage, base string) (Customer, error) {
	var w wireCustomer
	if err := json.Unmarshal(raw, &w); err != nil {
		return Customer{}, err
	}
	c := Customer{ID: w.ID, DisplayName: strings.TrimSpace(w.DisplayName), Currency: currencyOf(w.CurrencyRef, base), Active: w.Active == nil || *w.Active,
		UpdatedAt: parseMeta(w.MetaData), Raw: raw}
	if c.DisplayName == "" {
		c.DisplayName = strings.TrimSpace(w.CompanyName)
	}
	if w.PrimaryEmailAddr != nil {
		c.Email = strings.TrimSpace(w.PrimaryEmailAddr.Address)
	}
	if w.PrimaryPhone != nil {
		c.Phone = strings.TrimSpace(w.PrimaryPhone.FreeFormNumber)
	}
	if a := w.BillAddr; a != nil {
		c.Line1, c.City, c.Region, c.PostalCode, c.Country = a.Line1, a.City, a.CountrySubDivisionCode, a.PostalCode, a.Country
	}
	return c, nil
}

func decodeInvoice(raw json.RawMessage, base string) (Invoice, error) {
	var w wireInvoice
	if err := json.Unmarshal(raw, &w); err != nil {
		return Invoice{}, err
	}
	txn, err := parseDate(w.TxnDate)
	if err != nil {
		return Invoice{}, fmt.Errorf("invoice %s: invalid TxnDate %q", w.ID, w.TxnDate)
	}
	inv := Invoice{ID: w.ID, DocNumber: strings.TrimSpace(w.DocNumber), CustomerID: w.CustomerRef.Value, CustomerName: w.CustomerRef.Name,
		TxnDate: txn, Total: w.TotalAmt, Balance: w.Balance, Currency: currencyOf(w.CurrencyRef, base), UpdatedAt: parseMeta(w.MetaData), Raw: raw}
	if w.DueDate != "" {
		if d, err := parseDate(w.DueDate); err == nil {
			inv.DueDate = &d
		}
	}
	if w.TxnTaxDetail != nil {
		inv.TotalTax = w.TxnTaxDetail.TotalTax
	}
	for _, l := range w.Line {
		if l.DetailType != "SalesItemLineDetail" {
			continue
		}
		line := InvoiceLine{Description: l.Description, Amount: l.Amount}
		if l.SalesItemLineDetail != nil {
			line.Quantity, line.UnitPrice = l.SalesItemLineDetail.Qty, l.SalesItemLineDetail.UnitPrice
		}
		inv.Lines = append(inv.Lines, line)
	}
	return inv, nil
}

func decodePayment(raw json.RawMessage, base string) (Payment, error) {
	var w wirePayment
	if err := json.Unmarshal(raw, &w); err != nil {
		return Payment{}, err
	}
	txn, err := parseDate(w.TxnDate)
	if err != nil {
		return Payment{}, fmt.Errorf("payment %s: invalid TxnDate %q", w.ID, w.TxnDate)
	}
	p := Payment{ID: w.ID, CustomerID: w.CustomerRef.Value, TxnDate: txn, Total: w.TotalAmt, Currency: currencyOf(w.CurrencyRef, base),
		UpdatedAt: parseMeta(w.MetaData), Raw: raw}
	for _, l := range w.Line {
		for _, lt := range l.LinkedTxn {
			if lt.TxnType == "Invoice" {
				p.Applied = append(p.Applied, PaymentApplication{InvoiceID: lt.TxnID, Amount: l.Amount})
			}
		}
	}
	return p, nil
}

// Validation of incoming records. Invalid records are skipped and counted, never stored.

func ValidateCustomer(c Customer) error {
	if c.ID == "" {
		return errors.New("customer without Id")
	}
	if c.DisplayName == "" {
		return fmt.Errorf("customer %s has no name", c.ID)
	}
	return nil
}

func ValidateInvoice(i Invoice) error {
	switch {
	case i.ID == "":
		return errors.New("invoice without Id")
	case i.CustomerID == "":
		return fmt.Errorf("invoice %s has no customer", i.ID)
	case i.Total.IsNegative():
		return fmt.Errorf("invoice %s has a negative total", i.ID)
	case i.Balance.IsNegative() || i.Balance.GreaterThan(i.Total):
		return fmt.Errorf("invoice %s has an invalid balance", i.ID)
	case len(i.Currency) != 3:
		return fmt.Errorf("invoice %s has an invalid currency", i.ID)
	}
	return nil
}

func ValidatePayment(p Payment) error {
	switch {
	case p.ID == "":
		return errors.New("payment without Id")
	case !p.Total.IsPositive():
		return fmt.Errorf("payment %s has a non-positive amount", p.ID)
	case len(p.Currency) != 3:
		return fmt.Errorf("payment %s has an invalid currency", p.ID)
	}
	var applied decimal.Decimal
	for _, a := range p.Applied {
		if a.Amount.IsNegative() {
			return fmt.Errorf("payment %s applies a negative amount", p.ID)
		}
		applied = applied.Add(a.Amount)
	}
	if applied.GreaterThan(p.Total) {
		return fmt.Errorf("payment %s applies more than its total", p.ID)
	}
	return nil
}
