package service

import (
	"bytes"
	"html/template"

	"campusassistant-api/internal/domain"
)

var invoiceTmpl = template.Must(template.New("invoice").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Invoice {{.Number}}</title>
<style>
 body{font:14px/1.5 -apple-system,Segoe UI,Roboto,sans-serif;color:#1a1a1a;max-width:720px;margin:32px auto;padding:0 16px}
 h1{font-size:22px;margin:0 0 4px} .muted{color:#666} .row{display:flex;justify-content:space-between;gap:16px;margin:16px 0}
 table{width:100%;border-collapse:collapse;margin-top:16px} th,td{padding:8px;border-bottom:1px solid #e5e5e5;text-align:left}
 th.n,td.n{text-align:right} tfoot td{border:0;font-weight:600} .total td{font-size:16px}
 @media print{body{margin:0}}
</style></head><body>
<h1>Campus Assistant</h1>
<div class="muted">Invoice {{.Number}} &middot; {{.IssuedAt.Format "2 Jan 2006"}}</div>
{{if .VoidedAt}}<div style="color:#b00020;font-weight:700;margin-top:8px">VOID &mdash; refunded {{.VoidedAt.Format "2 Jan 2006"}}</div>{{end}}
<div class="row">
 <div><strong>Billed to</strong><br>{{.CustomerName}}<br>{{.CustomerEmail}}{{if .CustomerPhone}}<br>{{.CustomerPhone}}{{end}}</div>
 <div><strong>Payment</strong><br>{{.PaymentMethod}}{{if .PaymentRef}}<br>Ref: {{.PaymentRef}}{{end}}</div>
</div>
<table>
 <thead><tr><th>Description</th><th class="n">Qty</th><th class="n">Unit ({{.Currency}})</th><th class="n">Amount ({{.Currency}})</th></tr></thead>
 <tbody>{{range .Lines}}<tr><td>{{.Description}}</td><td class="n">{{.Quantity}}</td><td class="n">{{.UnitPrice}}</td><td class="n">{{.Total}}</td></tr>{{end}}</tbody>
 <tfoot>
  {{if .Discount}}<tr><td colspan="3" class="n">Subtotal</td><td class="n">{{.Subtotal}}</td></tr>
  <tr><td colspan="3" class="n">Discount</td><td class="n">-{{.Discount}}</td></tr>{{end}}
  <tr class="total"><td colspan="3" class="n">Total paid</td><td class="n">{{.Currency}} {{.Total}}</td></tr>
 </tfoot>
</table>
<p class="muted">Thank you. This is a computer-generated invoice and needs no signature.</p>
</body></html>`))

// RenderInvoiceHTML renders a printable invoice page (use the browser's
// "Save as PDF"). html/template escapes every customer-supplied field.
func RenderInvoiceHTML(inv *domain.Invoice) (string, error) {
	var buf bytes.Buffer
	if err := invoiceTmpl.Execute(&buf, inv); err != nil {
		return "", err
	}
	return buf.String(), nil
}
