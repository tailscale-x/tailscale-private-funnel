package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

func (c *Control) policies(w http.ResponseWriter, r *http.Request) {
	d := c.loadExposurePolicy()
	admin := requestAccess(r).Admin
	choices := map[string]map[string]string{"ingress_ids": {}, "loadbalancer_ids": {}}
	ingressNodes, _ := c.Store.Nodes("ingress")
	for _, node := range ingressNodes {
		if node["revoked"] == true {
			continue
		}
		id := stringValue(node, "id", "")
		choices["ingress_ids"][id] = stringValue(node, "name", id) + " · " + stringValue(node, "hostname", id)
	}
	loadbalancers, _ := c.Store.Nodes("loadbalancer")
	for _, node := range loadbalancers {
		if node["revoked"] == true {
			continue
		}
		deployment, err := c.nodeDeployment(stringValue(node, "id", ""))
		if err == nil && deployment.LoadBalancerHostname != "" {
			choices["loadbalancer_ids"][stringValue(node, "id", "")] = stringValue(node, "name", deployment.LoadBalancerHostname) + " · " + deployment.LoadBalancerHostname
		}
	}
	// Preserve rule values even when the referenced node is no longer active.
	for _, rule := range d.Rules {
		for _, id := range rule.IngressIDs {
			if _, ok := choices["ingress_ids"][id]; !ok {
				choices["ingress_ids"][id] = id + " (not active)"
			}
		}
		for _, hostname := range rule.LoadBalancerIDs {
			if _, ok := choices["loadbalancer_ids"][hostname]; !ok {
				choices["loadbalancer_ids"][hostname] = hostname + " (not active)"
			}
		}
	}
	selector := func(key, label string) string {
		var html strings.Builder
		html.WriteString(`<label class="pf-field"><span class="pf-label">` + label + `</span><select class="pf-input" name="` + key + `" multiple aria-describedby="` + key + `-help">`)
		keys := make([]string, 0, len(choices[key]))
		for value := range choices[key] {
			keys = append(keys, value)
		}
		sort.Strings(keys)
		for _, value := range keys {
			fmt.Fprintf(&html, `<option value="%s">%s</option>`, safeText(value), safeText(choices[key][value]))
		}
		html.WriteString(`</select><small class="pf-help" id="` + key + `-help">No selection means any. Select multiple with Ctrl or Command; tap options on mobile.</small></label>`)
		return html.String()
	}
	var b strings.Builder
	b.WriteString(pageIntro("Access", "Exposure policy", "Rules authorize public hostnames for Docker and tailnet backends. Default deny applies; another enabled matching rule can preserve an exposure."))
	var status struct {
		Pending bool   `json:"pending"`
		Message string `json:"message"`
	}
	if c.Store.GetJSON("policy_cleanup", &status) == nil {
		b.WriteString(`<div class="pf-alert pf-alert-info" role="status">` + safeText(status.Message) + `</div>`)
	}
	b.WriteString(`<section class="pf-surface"><div class="pf-section-heading"><h2>Rules</h2></div><div class="pf-table-wrap"><table class="pf-table"><thead><tr><th>Rule</th><th>Hostname patterns</th><th>Selectors</th><th>Load balancers</th><th>State</th><th>Actions</th></tr></thead><tbody>`)
	if len(d.Rules) == 0 {
		b.WriteString(`<tr><td colspan="6"><div class="pf-empty"><strong>No rules configured</strong><span>Default deny is active. Add a rule to authorize Docker labels.</span></div></td></tr>`)
	}
	for _, rule := range d.Rules {
		state := "Disabled"
		if rule.Enabled {
			state = "Enabled"
		}
		actions := "Read only"
		if admin {
			actions = `<div class="pf-inline-actions"><button class="pf-link-button" data-edit="` + safeText(rule.ID) + `">Edit</button> <button class="pf-link-button" data-toggle="` + safeText(rule.ID) + `">` + map[bool]string{true: "Disable", false: "Enable"}[rule.Enabled] + `</button> <button class="pf-link-button pf-link-danger" data-delete="` + safeText(rule.ID) + `">Delete</button></div>`
		}
		selectors := strings.Join(rule.Roles, ", ")
		for _, id := range rule.IngressIDs {
			if selectors != "" {
				selectors += ", "
			}
			selectors += c.nodeDisplayName(id)
		}
		if strings.TrimSpace(selectors) == "" {
			selectors = "All enrolled ingress nodes"
		}
		loadbalancers := ""
		for _, id := range rule.LoadBalancerIDs {
			if loadbalancers != "" {
				loadbalancers += ", "
			}
			loadbalancers += c.nodeDisplayName(id)
		}
		if strings.TrimSpace(loadbalancers) == "" {
			loadbalancers = "All load balancers"
		}
		var patterns strings.Builder
		for _, pattern := range rule.HostnameRegexes {
			patterns.WriteString("<div><code>" + safeText(pattern) + "</code></div>")
		}
		fmt.Fprintf(&b, `<tr><td data-label="Rule">%s</td><td data-label="Hostname patterns">%s</td><td data-label="Selectors">%s</td><td data-label="Load balancers">%s</td><td data-label="State">%s</td><td data-label="Actions">%s</td></tr>`, safeText(rule.Name), patterns.String(), safeText(selectors), safeText(loadbalancers), state, actions)
	}
	b.WriteString(`</tbody></table></div></section>`)
	if admin {
		b.WriteString(`<section class="pf-surface pf-form-surface" id="rule-editor"><div class="pf-section-heading"><h2 id="editor-heading">Add an authorization rule</h2></div><form id="rule-form" class="pf-form-grid">`)
		for _, f := range []struct{ Key, Label string }{{"name", "Rule name"}, {"roles", "Tailscale roles / tags (optional, comma-separated)"}} {
			b.WriteString(`<label class="pf-field"><span class="pf-label">` + f.Label + `</span><input class="pf-input" name="` + f.Key + `"></label>`)
		}
		b.WriteString(selector("ingress_ids", "Ingress nodes (optional)") + selector("loadbalancer_ids", "Load balancers (optional)"))
		b.WriteString(`<fieldset class="pf-field-wide"><legend class="pf-label">Hostname patterns</legend><p class="pf-help">Match any pattern below against the complete hostname. At least one pattern is required. Each row is one regular expression; commas stay part of the expression.</p><div id="hostname-patterns"></div><button type="button" class="pf-secondary-button" id="add-pattern">Add pattern</button><p id="pattern-status" class="pf-help" role="status"></p></fieldset>`)
		b.WriteString(`<p class="pf-help pf-field-wide">Leave roles and ingress node IDs blank to allow all enrolled ingress nodes matching this hostname rule. If either is filled, a matching role or node ID is required. Leave load balancers unselected to allow any load balancer.</p><label class="pf-check-field"><input type="checkbox" name="enabled" checked> Enabled</label><div class="pf-form-actions"><button class="pf-button" id="rule-save">Save rule</button><button class="pf-secondary-button" id="rule-cancel" type="button">Cancel</button></div></form><p id="policy-result" role="status"></p></section><section class="pf-surface pf-form-surface"><details><summary>Advanced JSON import / export</summary><label class="pf-field"><span class="pf-label">Policy document</span><textarea class="pf-input font-mono" id="policy-json" rows="16">`)
		raw, _ := json.MarshalIndent(d, "", "  ")
		b.WriteString(safeText(string(raw)))
		b.WriteString(`</textarea></label><div class="pf-form-actions"><button class="pf-button" id="json-save">Save policy</button><button class="pf-secondary-button" id="json-validate">Validate only</button><button class="pf-secondary-button" id="json-format">Format JSON</button></div><pre id="json-result" role="status"></pre></details></section>`)
		raw, _ = json.Marshal(d)
		b.WriteString(`<script>const policy=` + string(raw) + `;let editing='';const form=document.getElementById('rule-form'), feedback=document.getElementById('policy-result');
async function api(url,method,body){const r=await fetch(url,{method,headers:{'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});const text=await r.text();if(!r.ok)throw Error(text);return text?JSON.parse(text):{};}
function reset(){editing='';form.reset();setPatterns(['']);document.getElementById('editor-heading').textContent='Add an authorization rule';document.getElementById('rule-save').textContent='Save rule';}
const patterns=document.getElementById('hostname-patterns'),patternStatus=document.getElementById('pattern-status');
function updatePatterns(){const rows=patterns.querySelectorAll('[data-pattern-row]');rows.forEach((row,i)=>{row.querySelector('span').textContent='Pattern '+(i+1);const remove=row.querySelector('button');remove.disabled=rows.length===1;remove.setAttribute('aria-label','Remove pattern '+(i+1));});document.getElementById('add-pattern').disabled=rows.length>=50;}
function addPattern(value='',focus=false){if(patterns.children.length>=50)return;const row=document.createElement('div');row.className='pf-pattern-row';row.dataset.patternRow='';const label=document.createElement('label');label.className='pf-field';const caption=document.createElement('span');caption.className='pf-label';const input=document.createElement('input');input.className='pf-input font-mono';input.name='hostname_regexes';input.required=true;input.value=value;input.placeholder='[^.]+[.]example[.]com';label.append(caption,input);const remove=document.createElement('button');remove.type='button';remove.className='pf-secondary-button';remove.textContent='Remove';remove.onclick=()=>{const next=row.nextElementSibling||row.previousElementSibling;row.remove();updatePatterns();next?.querySelector('input').focus();patternStatus.textContent='Pattern removed.';};row.append(label,remove);patterns.append(row);updatePatterns();if(focus)input.focus();}
function setPatterns(values){patterns.replaceChildren();for(const value of values?.length?values:[''])addPattern(value);patternStatus.textContent='';}
document.getElementById('add-pattern').onclick=()=>{addPattern('',true);patternStatus.textContent='Pattern added.';};setPatterns(['']);
async function confirmChange(next){const p=await api('/api/policies/preview','POST',next);return confirm('Affected exposures: '+(p.affected_exposures.join(', ')||'None')+'\nSave this change? Denied routes are withdrawn on polling; owned DNS cleanup is queued and retried.');}
async function change(rule,id){const next=structuredClone(policy);if(id){const i=next.rules.findIndex(x=>x.id===id);if(rule)next.rules[i]=rule;else next.rules.splice(i,1);}else next.rules.push(rule);if(!await confirmChange(next))return;if(id&&!rule)await api('/api/policies/rules/'+encodeURIComponent(id)+'?revision='+policy.revision,'DELETE');else await api(id?'/api/policies/rules/'+encodeURIComponent(id):'/api/policies/rule',id?'PUT':'POST',{revision:policy.revision,rule});location.reload();}
document.querySelectorAll('[data-edit]').forEach(el=>el.onclick=()=>{editing=el.dataset.edit;const x=policy.rules.find(x=>x.id===editing);setPatterns(x.hostname_regexes);for(const key of ['name','roles'])form.elements[key].value=Array.isArray(x[key])?x[key].join(', '):(x[key]||'');for(const key of ['ingress_ids','loadbalancer_ids'])for(const option of form.elements[key].options)option.selected=(x[key]||[]).includes(option.value);form.elements.enabled.checked=x.enabled;document.getElementById('editor-heading').textContent='Edit authorization rule';document.getElementById('rule-save').textContent='Save changes';document.getElementById('rule-editor').scrollIntoView();form.elements.name.focus();});
document.querySelectorAll('[data-toggle]').forEach(el=>el.onclick=async()=>{try{const x=structuredClone(policy.rules.find(x=>x.id===el.dataset.toggle));x.enabled=!x.enabled;await change(x,x.id);}catch(e){feedback.textContent=e.message;}});
document.querySelectorAll('[data-delete]').forEach(el=>el.onclick=async()=>{try{await change(null,el.dataset.delete);}catch(e){feedback.textContent=e.message;}});
form.onsubmit=async e=>{e.preventDefault();try{const rule={enabled:form.elements.enabled.checked};rule.name=form.elements.name.value.trim();rule.hostname_regexes=Array.from(document.querySelectorAll('[name=hostname_regexes]')).map(input=>input.value);rule.roles=form.elements.roles.value.split(',').map(x=>x.trim()).filter(Boolean);for(const key of ['ingress_ids','loadbalancer_ids'])rule[key]=Array.from(form.elements[key].selectedOptions,option=>option.value);await change(rule,editing);}catch(e){feedback.textContent=e.message;}};document.getElementById('rule-cancel').onclick=reset;
const jsonEditor=document.getElementById('policy-json'),jsonResult=document.getElementById('json-result');
function formatJSON(){const d=JSON.parse(jsonEditor.value);jsonEditor.value=JSON.stringify(d,null,2);return d;}
document.getElementById('json-format').onclick=()=>{try{formatJSON();jsonResult.textContent='JSON formatted. No changes saved.';}catch(e){jsonResult.textContent=e.message;}};
jsonEditor.addEventListener('blur',()=>{try{formatJSON();}catch(_){}});
jsonEditor.addEventListener('paste',()=>setTimeout(()=>{try{formatJSON();}catch(_){}},0));
document.getElementById('json-validate').onclick=async()=>{try{jsonResult.textContent=JSON.stringify(await api('/api/policies/validate','POST',formatJSON()),null,2);}catch(e){jsonResult.textContent=e.message;}};
document.getElementById('json-save').onclick=async()=>{try{const d=formatJSON();if(await confirmChange(d)){await api('/api/policies','POST',d);location.reload();}}catch(e){jsonResult.textContent=e.message;}};</script>`)
	}
	b.WriteString(`<section class="pf-surface pf-form-surface"><div class="pf-section-heading"><h2>Test policy</h2></div><form class="pf-form-grid" hx-post="/api/policies/test" hx-target="#policy-test-result">`)
	for _, key := range []string{"hostname", "loadbalancer_id", "roles", "ingress_ids"} {
		choiceKey := key
		if key == "loadbalancer_id" {
			choiceKey = "loadbalancer_ids"
		}
		if options, ok := choices[choiceKey]; ok {
			b.WriteString(`<label class="pf-field"><span class="pf-label">` + safeText(map[string]string{"hostname": "Public hostname", "loadbalancer_id": "Load balancer", "roles": "Tailscale roles / tags", "ingress_ids": "Ingress node"}[key]) + `</span><select class="pf-input" name="` + key + `"><option value="">Select for this test</option>`)
			values := make([]string, 0, len(options))
			for value := range options {
				values = append(values, value)
			}
			sort.Strings(values)
			for _, value := range values {
				fmt.Fprintf(&b, `<option value="%s">%s</option>`, safeText(value), safeText(options[value]))
			}
			b.WriteString(`</select></label>`)
		} else {
			b.WriteString(`<label class="pf-field"><span class="pf-label">` + safeText(map[string]string{"hostname": "Public hostname", "loadbalancer_id": "Load balancer", "roles": "Tailscale roles / tags", "ingress_ids": "Ingress node"}[key]) + `</span><input class="pf-input" name="` + key + `"></label>`)
		}
	}
	b.WriteString(`<button class="pf-secondary-button">Test policy</button></form><pre id="policy-test-result" role="status"></pre></section>`)
	render(w, "Exposure policy", b.String())
}
