(function () {
  const motionContainer = document.getElementById("motion-trails");
  const TRAIL_COUNT = 16;

  function createTrail() {
    if (!motionContainer) return null;
    const span = document.createElement("span");
    span.className = "trail";
    span.style.top = `${Math.random() * 100}%`;
    span.style.animationDuration = `${12 + Math.random() * 14}s`;
    span.style.animationDelay = `${Math.random() * -18}s`;
    span.style.opacity = (0.35 + Math.random() * 0.4).toFixed(2);
    span.style.transform = `scaleX(${0.7 + Math.random() * 0.5})`;
    motionContainer.appendChild(span);
    return span;
  }

  function initTrails() {
    if (!motionContainer) return;
    for (let i = 0; i < TRAIL_COUNT; i += 1) {
      createTrail();
    }
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", initTrails);
  } else {
    initTrails();
  }
})();
