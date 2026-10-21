// agent-in-browser page library. Injected before page-level tool calls; it is
// idempotent and protocol-neutral (works through any driver's Evaluate).
(() => {
  const VERSION = 4;
  if (window.__aib && window.__aib.v === VERSION) return;

  const ROLE_BY_TAG = {
    A: 'link', BUTTON: 'button', SELECT: 'combobox', TEXTAREA: 'textbox', SUMMARY: 'button',
    H1: 'heading', H2: 'heading', H3: 'heading', H4: 'heading', H5: 'heading', H6: 'heading',
    IMG: 'img', NAV: 'navigation', MAIN: 'main', HEADER: 'banner', FOOTER: 'contentinfo',
    FORM: 'form', DIALOG: 'dialog', ASIDE: 'complementary', TABLE: 'table', IFRAME: 'iframe',
    OPTION: 'option', LABEL: 'label',
  };
  const INPUT_ROLE = {
    button: 'button', submit: 'button', reset: 'button', image: 'button', checkbox: 'checkbox',
    radio: 'radio', range: 'slider', number: 'spinbutton', search: 'searchbox', email: 'textbox',
    tel: 'textbox', text: 'textbox', url: 'textbox', password: 'textbox', file: 'button',
  };
  const INTERACTIVE_ROLES = new Set(['button', 'link', 'checkbox', 'radio', 'tab', 'menuitem',
    'menuitemcheckbox', 'menuitemradio', 'option', 'switch', 'textbox', 'combobox', 'searchbox',
    'slider', 'spinbutton', 'treeitem', 'listbox']);
  const LANDMARKS = new Set(['navigation', 'main', 'banner', 'contentinfo', 'form', 'dialog',
    'complementary', 'region', 'search', 'table', 'list', 'iframe']);
  const TEXT_TAGS = new Set(['P', 'LI', 'TD', 'TH', 'DT', 'DD', 'BLOCKQUOTE', 'PRE', 'FIGCAPTION', 'CAPTION', 'SPAN', 'DIV', 'STRONG', 'EM', 'CODE']);
  const SKIP_TAGS = new Set(['SCRIPT', 'STYLE', 'NOSCRIPT', 'TEMPLATE', 'SVG', 'HEAD', 'META', 'LINK']);

  const clean = (s, max = 100) => {
    s = String(s || '').replace(/\s+/g, ' ').trim();
    return s.length > max ? s.slice(0, max - 1) + '…' : s;
  };

  function role(el) {
    const explicit = el.getAttribute('role');
    if (explicit) return explicit.split(' ')[0];
    if (el.tagName === 'INPUT') return INPUT_ROLE[(el.type || 'text').toLowerCase()] || 'textbox';
    if (el.tagName === 'A' && !el.hasAttribute('href')) return 'generic';
    if (el.tagName === 'SECTION' && (el.getAttribute('aria-label') || el.getAttribute('aria-labelledby'))) return 'region';
    if (el.tagName === 'UL' || el.tagName === 'OL') return 'list';
    if (el.isContentEditable && el.getAttribute('contenteditable') !== null) return 'textbox';
    return ROLE_BY_TAG[el.tagName] || 'generic';
  }

  function name(el) {
    const doc = el.ownerDocument;
    const aria = el.getAttribute('aria-label');
    if (aria) return clean(aria);
    const by = el.getAttribute('aria-labelledby');
    if (by) {
      const t = by.split(/\s+/).map((id) => doc.getElementById(id)).filter(Boolean).map((n) => n.innerText || n.textContent).join(' ');
      if (t.trim()) return clean(t);
    }
    if (el.labels && el.labels.length) return clean(Array.from(el.labels).map((l) => l.innerText || l.textContent).join(' '));
    if (el.tagName === 'IMG') return clean(el.alt || el.title);
    if (el.tagName === 'INPUT' && ['submit', 'button', 'reset'].includes(el.type)) return clean(el.value);
    if (el.tagName === 'SELECT') {
      const o = el.selectedOptions && el.selectedOptions[0];
      return clean(el.getAttribute('placeholder') || el.title || (o ? '' : ''));
    }
    const txt = ['INPUT', 'TEXTAREA'].includes(el.tagName) ? '' : (el.innerText || el.textContent);
    return clean(txt || el.getAttribute('placeholder') || el.title || el.getAttribute('alt') || '');
  }

  function isVisible(el) {
    const view = el.ownerDocument.defaultView;
    const style = view.getComputedStyle(el);
    if (style.display === 'none' || style.visibility === 'hidden' || style.visibility === 'collapse') return false;
    if (parseFloat(style.opacity) === 0 && el.tagName !== 'INPUT') return false;
    const r = el.getBoundingClientRect();
    return r.width > 0 && r.height > 0;
  }

  function isInteractive(el) {
    const r = role(el);
    if (INTERACTIVE_ROLES.has(r)) return true;
    if (el.isContentEditable) return true;
    if (el.hasAttribute('onclick')) return true;
    const ti = el.getAttribute('tabindex');
    if (ti !== null && Number(ti) >= 0) return true;
    try {
      return el.ownerDocument.defaultView.getComputedStyle(el).cursor === 'pointer' &&
        !(el.parentElement && el.ownerDocument.defaultView.getComputedStyle(el.parentElement).cursor === 'pointer');
    } catch (_) { return false; }
  }

  function refOf(el) {
    let r = el.getAttribute('data-aib-ref');
    if (!r) {
      window.__aibSeq = (window.__aibSeq || 0) + 1;
      r = 'e' + window.__aibSeq;
      el.setAttribute('data-aib-ref', r);
    }
    return r;
  }

  // Children including open shadow roots and same-origin iframe documents.
  function children(el) {
    const out = [];
    if (el.shadowRoot) out.push(...el.shadowRoot.children);
    if (el.tagName === 'IFRAME' || el.tagName === 'FRAME') {
      try { if (el.contentDocument && el.contentDocument.body) out.push(el.contentDocument.body); } catch (_) {}
    }
    if (el.tagName === 'SLOT') out.push(...el.assignedElements());
    out.push(...el.children);
    return out;
  }

  function* walk(root) {
    const stack = [root];
    while (stack.length) {
      const el = stack.pop();
      yield el;
      const kids = children(el);
      for (let i = kids.length - 1; i >= 0; i--) stack.push(kids[i]);
    }
  }

  function frameOffset(el) {
    let x = 0, y = 0, w = el.ownerDocument.defaultView;
    while (w && w !== window && w.frameElement) {
      const r = w.frameElement.getBoundingClientRect();
      x += r.left + w.frameElement.clientLeft;
      y += r.top + w.frameElement.clientTop;
      w = w.parent;
    }
    return { x, y };
  }

  function describe(el) {
    const r = role(el);
    let s = r;
    const n = name(el);
    if (n) s += ' ' + JSON.stringify(n);
    return s;
  }

  function attrs(el, r) {
    const out = [];
    if (r === 'heading') out.push('level=' + el.tagName.slice(1));
    if (el.disabled || el.getAttribute('aria-disabled') === 'true') out.push('disabled');
    if (r === 'checkbox' || r === 'radio' || r === 'switch') out.push((el.checked || el.getAttribute('aria-checked') === 'true') ? 'checked' : 'unchecked');
    if (el.getAttribute('aria-expanded')) out.push('expanded=' + el.getAttribute('aria-expanded'));
    if (el.getAttribute('aria-selected') === 'true') out.push('selected');
    if (el.required) out.push('required');
    if (el.tagName === 'INPUT' && el.type && !['text', 'submit', 'button', 'checkbox', 'radio'].includes(el.type)) out.push('type=' + el.type);
    if (['INPUT', 'TEXTAREA'].includes(el.tagName) && el.type !== 'password' && el.value && !['submit', 'button', 'checkbox', 'radio'].includes(el.type)) out.push('value=' + JSON.stringify(clean(el.value, 60)));
    if (el.tagName === 'INPUT' && el.placeholder && !name(el).includes(el.placeholder)) out.push('placeholder=' + JSON.stringify(clean(el.placeholder, 40)));
    if (el.tagName === 'SELECT') {
      const o = el.selectedOptions && el.selectedOptions[0];
      if (o) out.push('selected=' + JSON.stringify(clean(o.label || o.text, 40)));
      out.push('options=' + el.options.length);
    }
    if (el.tagName === 'A' && el.href && !el.href.startsWith('javascript:')) out.push('href=' + clean(el.getAttribute('href'), 80));
    const rect = el.getBoundingClientRect(), off = frameOffset(el);
    const vh = window.innerHeight, vw = window.innerWidth;
    const top = rect.top + off.y, left = rect.left + off.x;
    if (top > vh || top + rect.height < 0 || left > vw || left + rect.width < 0) out.push('offscreen');
    return out;
  }

  function snapshot(opts) {
    opts = opts || {};
    const mode = opts.mode || 'interactive';
    const maxChars = opts.maxChars || 20000;
    let root = document.body || document.documentElement;
    if (opts.selector) {
      root = resolve({ selector: opts.selector });
    }
    const lines = [];
    let size = 0, truncated = false, count = 0;
    const seenText = new Set();

    function emit(depth, line) {
      if (truncated) return;
      const l = '  '.repeat(depth) + '- ' + line;
      if (size + l.length > maxChars) { truncated = true; return; }
      lines.push(l); size += l.length + 1;
    }

    function visit(el, depth) {
      if (truncated || !el || SKIP_TAGS.has(el.tagName)) return;
      if (el.getAttribute && el.getAttribute('aria-hidden') === 'true') return;
      let visible = true;
      try { visible = isVisible(el); } catch (_) {}
      const r = role(el);
      // display:contents and zero-size wrappers can still hold visible children.
      let nextDepth = depth;
      if (visible) {
        if (isInteractive(el)) {
          const a = attrs(el, r);
          emit(depth, describe(el) + ' [ref=' + refOf(el) + ']' + (a.length ? ' (' + a.join(', ') + ')' : ''));
          count++;
          // Real controls: their name already covers inner text. Generic
          // clickables (cards, onclick divs) may still contain links/buttons.
          if (INTERACTIVE_ROLES.has(r) && r !== 'listbox' && !el.shadowRoot) return;
        } else if (r === 'heading') {
          emit(depth, describe(el) + ' (' + attrs(el, r).join(', ') + ')');
          return;
        } else if (r === 'img' && name(el)) {
          emit(depth, describe(el));
        } else if (LANDMARKS.has(r) && el !== root) {
          // Landmarks are named only explicitly; innerText would repeat children.
          const n = clean(el.getAttribute('aria-label') || '');
          emit(depth, r + (n && r !== 'list' && r !== 'table' && r !== 'form' && r !== 'main' && n.length < 60 ? ' ' + JSON.stringify(n) : ''));
          nextDepth = depth + 1;
        } else if (mode === 'all' && TEXT_TAGS.has(el.tagName)) {
          // Emit direct text only, so nested blocks are not repeated.
          let own = '';
          for (const n of el.childNodes) if (n.nodeType === 3) own += n.textContent;
          own = clean(own, 300);
          if (own.length > 1 && !seenText.has(own)) { seenText.add(own); emit(depth, 'text ' + JSON.stringify(own)); }
        }
      }
      for (const c of children(el)) visit(c, nextDepth);
    }

    visit(root, 0);
    const header = [
      'Page: ' + document.title,
      'URL: ' + location.href,
      'Viewport: ' + innerWidth + 'x' + innerHeight + ', scroll ' + Math.round(scrollY) + '/' + Math.max(0, document.documentElement.scrollHeight - innerHeight),
      'Interactive elements: ' + count + (truncated ? ' (output truncated; use selector= or max_chars=)' : ''),
      '',
    ];
    return header.concat(lines).join('\n');
  }

  function matchText(el, q) {
    const n = name(el).toLowerCase();
    return n && n.includes(q);
  }

  function resolve(t) {
    let el = null;
    if (t.ref) {
      for (const e of walk(document.documentElement)) {
        if (e.getAttribute && e.getAttribute('data-aib-ref') === t.ref) { el = e; break; }
      }
      if (!el) throw new Error('ref ' + t.ref + ' not found; the page changed — take a new snapshot');
    } else if (t.selector) {
      el = document.querySelector(t.selector);
      if (!el) {
        for (const e of walk(document.documentElement)) {
          if (e.shadowRoot) { el = e.shadowRoot.querySelector(t.selector); if (el) break; }
        }
      }
      if (!el) throw new Error('no element matches selector ' + t.selector);
    } else if (t.match) {
      const q = String(t.match).toLowerCase().trim();
      let fallback = null, exact = null;
      for (const e of walk(document.body || document.documentElement)) {
        if (!e.getBoundingClientRect || SKIP_TAGS.has(e.tagName)) continue;
        if (!matchText(e, q)) continue;
        let vis = false;
        try { vis = isVisible(e); } catch (_) {}
        if (!vis) continue;
        if (isInteractive(e)) {
          if (name(e).toLowerCase() === q) { exact = e; break; }
          if (!el) el = e;
        } else if (!fallback || name(e).length <= name(fallback).length) fallback = e;
      }
      el = exact || el || fallback;
      if (!el) throw new Error('no visible element matching text ' + JSON.stringify(t.match));
    } else {
      throw new Error('specify ref, selector or match');
    }
    return el;
  }

  function point(t) {
    const el = resolve(t);
    el.scrollIntoView({ block: 'center', inline: 'center', behavior: 'instant' });
    const r = el.getBoundingClientRect(), off = frameOffset(el);
    const x = r.left + off.x + r.width / 2, y = r.top + off.y + r.height / 2;
    let covered = '';
    try {
      const top = document.elementFromPoint(x, y);
      if (top && top !== el && !el.contains(top) && !(top.shadowRoot) && el.ownerDocument === document) covered = describe(top);
    } catch (_) {}
    return { x, y, width: r.width, height: r.height, desc: describe(el), ref: refOf(el), covered };
  }

  function focus(t) {
    const el = resolve(t);
    el.scrollIntoView({ block: 'center', behavior: 'instant' });
    el.focus();
    return describe(el);
  }

  function clear(t) {
    const el = resolve(t);
    el.focus();
    if ('value' in el) {
      setNative(el, '');
      el.dispatchEvent(new Event('input', { bubbles: true }));
    } else if (el.isContentEditable) {
      el.textContent = '';
    }
    return describe(el);
  }

  function setNative(el, value) {
    const proto = Object.getPrototypeOf(el);
    const desc = Object.getOwnPropertyDescriptor(proto, 'value');
    if (desc && desc.set) desc.set.call(el, value); else el.value = value;
  }

  function setValue(t, value) {
    const el = resolve(t);
    el.scrollIntoView({ block: 'center', behavior: 'instant' });
    const r = role(el);
    if (el.tagName === 'SELECT') {
      const want = String(value).toLowerCase();
      const opt = Array.from(el.options).find((o) => o.value.toLowerCase() === want) ||
        Array.from(el.options).find((o) => (o.label || o.text).toLowerCase().trim() === want) ||
        Array.from(el.options).find((o) => (o.label || o.text).toLowerCase().includes(want));
      if (!opt) throw new Error('option ' + JSON.stringify(value) + ' not in ' + Array.from(el.options).map((o) => o.label || o.text).join(', '));
      el.value = opt.value;
    } else if (r === 'checkbox' || r === 'radio' || r === 'switch') {
      const want = value === true || value === 'true' || value === 'on' || value === 1 || value === '1';
      if (el.tagName === 'INPUT') { if (el.checked !== want) el.click(); }
      else if ((el.getAttribute('aria-checked') === 'true') !== want) el.click();
      return describe(el) + ' = ' + want;
    } else if (el.isContentEditable) {
      el.focus(); el.textContent = String(value);
    } else {
      el.focus();
      setNative(el, String(value));
    }
    el.dispatchEvent(new Event('input', { bubbles: true }));
    el.dispatchEvent(new Event('change', { bubbles: true }));
    return describe(el) + ' = ' + JSON.stringify(String(value));
  }

  function find(query, limit) {
    const q = String(query).toLowerCase().trim();
    const out = [];
    for (const e of walk(document.body || document.documentElement)) {
      if (out.length >= (limit || 20)) break;
      if (!e.getBoundingClientRect || SKIP_TAGS.has(e.tagName)) continue;
      let vis = false;
      try { vis = isVisible(e); } catch (_) {}
      if (!vis) continue;
      const interactive = isInteractive(e);
      const r = role(e);
      if (!interactive && r !== 'heading' && !TEXT_TAGS.has(e.tagName)) continue;
      const hay = (name(e) + ' ' + (e.getAttribute('placeholder') || '') + ' ' + (e.id || '') + ' ' + (e.getAttribute('name') || '')).toLowerCase();
      if (!hay.includes(q)) continue;
      if (!interactive) {
        // Only report the innermost text container.
        let own = '';
        for (const n of e.childNodes) if (n.nodeType === 3) own += n.textContent;
        if (!own.toLowerCase().includes(q)) continue;
      }
      out.push(describe(e) + ' [ref=' + refOf(e) + ']' + (interactive ? '' : ' (text)'));
    }
    return out.length ? out.join('\n') : 'No visible elements match ' + JSON.stringify(query);
  }

  function text(selector, maxChars) {
    const root = selector ? resolve({ selector }) : (document.body || document.documentElement);
    let t = (root.innerText || root.textContent || '').replace(/\n{3,}/g, '\n\n').trim();
    const max = maxChars || 30000;
    if (t.length > max) t = t.slice(0, max) + '\n… [truncated ' + (t.length - max) + ' chars]';
    return 'Page: ' + document.title + '\nURL: ' + location.href + '\n\n' + t;
  }

  function check(c) {
    let ok = true;
    if (c.text) ok = ok && (document.body ? document.body.innerText : '').includes(c.text);
    if (c.selector) {
      let el = null;
      try { el = document.querySelector(c.selector); } catch (_) {}
      ok = ok && !!el && isVisible(el);
    }
    if (c.url) ok = ok && location.href.includes(c.url);
    return c.gone ? !ok : ok;
  }

  function scroll(o) {
    if (o.ref || o.selector || o.match) {
      const el = resolve(o);
      el.scrollIntoView({ block: 'center', behavior: 'instant' });
      return 'Scrolled to ' + describe(el);
    }
    const target = o.within ? resolve({ selector: o.within }) : null;
    const dx = o.dx || 0, dy = o.dy || 0;
    if (target) target.scrollBy(dx, dy); else window.scrollBy(dx, dy);
    return 'Scrolled by ' + dx + ',' + dy + '; now at ' + Math.round(scrollX) + ',' + Math.round(scrollY) +
      ' of ' + Math.max(0, document.documentElement.scrollHeight - innerHeight);
  }

  window.__aib = { v: VERSION, snapshot, point, focus, clear, setValue, find, text, check, scroll };
})();
