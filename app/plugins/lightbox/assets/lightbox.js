// AFSC image lightbox: same-origin image links inside data-selector open in a native modal <dialog>. Links in one
// .gallery form a set. Without JS (or without <dialog>) the links open the image as usual.
(() => {
  'use strict'
  const me = document.currentScript
  if (!me || typeof HTMLDialogElement !== 'function' || !('showModal' in HTMLDialogElement.prototype))
    return
  const cfg = me.dataset
  const text = key => cfg[key] || ''
  const IMAGE_EXT = /\.(avif|gif|jpe?g|png|svg|webp)$/i

  let dlg, stage, img, errBox, errText, errLink, caption, count, closeBtn, prevBtn, nextBtn
  let set = []
  let index = 0
  let opener = null
  let startX = null
  let startY = 0
  let swiped = false

  function isTrigger(a) {
    if (a.children.length !== 1 || a.children[0].localName !== 'img')
      return false
    let u
    try {
      u = new URL(a.href)
    }
    catch {
      return false
    }
    return u.origin === location.origin && (u.pathname.startsWith('/m/') || IMAGE_EXT.test(u.pathname))
  }

  function inScope(a) {
    try {
      return Boolean(cfg.selector && a.closest(cfg.selector))
    }
    catch {
      return false
    }
  }

  function make(tag, cls, attrs) {
    const e = document.createElement(tag)
    if (cls)
      e.className = cls
    for (const [k, v] of Object.entries(attrs || {}))
      e.setAttribute(k, v)
    return e
  }

  function button(cls, label, glyph, onClick) {
    const b = make('button', `plg-lightbox__btn ${cls}`, { 'type': 'button', 'aria-label': label })
    const g = make('span', '', { 'aria-hidden': 'true' })
    g.textContent = glyph
    b.append(g)
    b.addEventListener('click', onClick)
    return b
  }

  function build() {
    if (dlg)
      return
    dlg = make('dialog', 'plg-lightbox', { 'aria-label': text('viewer') })
    closeBtn = button('plg-lightbox__close', text('close'), '×', () => dlg.close())
    prevBtn = button('plg-lightbox__prev', text('prev'), '‹', () => go(-1))
    nextBtn = button('plg-lightbox__next', text('next'), '›', () => go(1))
    stage = make('div', 'plg-lightbox__stage')
    img = make('img', 'plg-lightbox__img', { alt: '', draggable: 'false' })
    errBox = make('p', 'plg-lightbox__error')
    errText = make('span')
    errLink = make('a')
    errLink.textContent = text('open')
    errBox.append(errText, ' ', errLink)
    errBox.hidden = true
    stage.append(make('div', 'plg-lightbox__spinner', { 'aria-hidden': 'true' }), img, errBox)
    caption = make('p', 'plg-lightbox__caption')
    count = make('p', 'plg-lightbox__count', { 'aria-live': 'polite' })
    dlg.append(closeBtn, stage, caption, count, prevBtn, nextBtn)

    img.addEventListener('load', () => dlg.classList.remove('is-loading'))
    img.addEventListener('error', () => {
      if (!img.getAttribute('src'))
        return
      dlg.classList.remove('is-loading')
      img.hidden = true
      errText.textContent = img.alt
      errLink.href = img.src
      errBox.hidden = false
    })
    dlg.addEventListener('close', () => {
      img.removeAttribute('src')
      const back = opener
      opener = null
      if (back && back.isConnected)
        back.focus()
    })
    dlg.addEventListener('keydown', (e) => {
      if (set.length < 2 || e.altKey || e.ctrlKey || e.metaKey)
        return
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault()
        go(e.key === 'ArrowLeft' ? -1 : 1)
      }
    })
    dlg.addEventListener('click', (e) => {
      if (swiped) {
        swiped = false
        return
      }
      if (e.target === dlg || e.target === stage)
        dlg.close()
    })
    dlg.addEventListener('pointerdown', (e) => {
      swiped = false
      startX = e.pointerType === 'mouse' ? null : e.clientX
      startY = e.clientY
    })
    dlg.addEventListener('pointerup', (e) => {
      if (startX === null)
        return
      const dx = e.clientX - startX
      const dy = e.clientY - startY
      startX = null
      if (set.length > 1 && Math.abs(dx) >= 50 && Math.abs(dx) > Math.abs(dy)) {
        swiped = true
        go(dx < 0 ? 1 : -1)
      }
    })
    dlg.addEventListener('pointercancel', () => {
      startX = null
    })
    document.body.append(dlg)
  }

  function show() {
    const a = set[index]
    const thumb = a.querySelector('img')
    const alt = (thumb && thumb.getAttribute('alt')) || ''
    const fig = a.closest('figure')
    const fc = fig && fig.querySelector('figcaption')
    const cap = fc ? fc.textContent.trim() : alt
    errBox.hidden = true
    img.hidden = false
    img.alt = alt
    dlg.classList.add('is-loading')
    img.src = a.href
    if (img.complete && img.naturalWidth > 0)
      dlg.classList.remove('is-loading')
    caption.textContent = cap
    caption.hidden = !cap
    const many = set.length > 1
    prevBtn.hidden = !many
    nextBtn.hidden = !many
    count.textContent = many ? text('counter').replace('{n}', String(index + 1)).replace('{total}', String(set.length)) : ''
  }

  function go(step) {
    index = (index + step + set.length) % set.length
    show()
  }

  function open(a) {
    build()
    const gallery = a.closest('.gallery')
    set = gallery ? Array.from(gallery.querySelectorAll('a[href]')).filter(isTrigger) : [a]
    index = Math.max(0, set.indexOf(a))
    opener = a
    show()
    dlg.showModal()
    closeBtn.focus()
  }

  document.addEventListener('click', (e) => {
    if (e.defaultPrevented || e.button !== 0 || e.metaKey || e.ctrlKey || e.shiftKey || e.altKey)
      return
    const a = e.target instanceof Element ? e.target.closest('a[href]') : null
    if (!a || !inScope(a) || !isTrigger(a))
      return
    e.preventDefault()
    open(a)
  })
})()
