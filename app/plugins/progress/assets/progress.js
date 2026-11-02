(function () {
  var bar = document.querySelector('.plg-progress__bar')
  if (!bar)
    return
  var ticking = false
  function update() {
    ticking = false
    var el = document.documentElement
    var max = el.scrollHeight - el.clientHeight
    var p = max > 0 ? Math.min(1, Math.max(0, el.scrollTop / max)) : 0
    bar.style.transform = 'scaleX(' + p + ')'
  }
  function schedule() {
    if (!ticking) {
      ticking = true
      requestAnimationFrame(update)
    }
  }
  addEventListener('scroll', schedule, { passive: true })
  addEventListener('resize', schedule, { passive: true })
  update()
})()
