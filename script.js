(function () {
  const iconContainer = document.getElementById("floating-icons");
  const icons = ["🔔", "🎮", "🌌", "🚀", "✨", "🛰️", "🎧", "🪐"];
  const iconCount = 18;
  const iconData = [];

  function createIcon(index) {
    const span = document.createElement("span");
    span.className = "floating-icon";
    span.textContent = icons[index % icons.length];
    iconContainer.appendChild(span);
    const speed = 0.3 + Math.random() * 0.6;
    const scale = 0.6 + Math.random() * 0.8;
    const drift = Math.random() * 24 + 12;
    const data = {
      element: span,
      baseX: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      scale,
      speed,
      drift,
      angle: Math.random() * Math.PI * 2,
    };
    iconData.push(data);
    return data;
  }

  function updateIcon(data) {
    data.y -= data.speed;
    data.angle += 0.0025 * data.speed * 60;
    if (data.y < -80) {
      data.y = window.innerHeight + Math.random() * 120;
      data.baseX = Math.random() * window.innerWidth;
    }
    const offsetX = Math.sin(data.angle) * data.drift;
    data.element.style.transform = `translate3d(${data.baseX + offsetX}px, ${data.y}px, 0) scale(${data.scale})`;
  }

  function animateIcons() {
    iconData.forEach(updateIcon);
    requestAnimationFrame(animateIcons);
  }

  function initIcons() {
    if (!iconContainer) return;
    for (let i = 0; i < iconCount; i += 1) {
      createIcon(i);
    }
    animateIcons();
  }

  function handleResize() {
    if (!iconData.length) return;
    iconData.forEach((item) => {
      item.baseX = Math.random() * window.innerWidth;
      item.y = Math.random() * window.innerHeight;
    });
  }

  const heroSection = document.querySelector(".hero");
  const heroCard = document.querySelector(".glass-card");

  function handleParallax(event) {
    if (!heroSection || !heroCard) return;
    const rect = heroSection.getBoundingClientRect();
    const relX = (event.clientX - rect.left) / rect.width - 0.5;
    const relY = (event.clientY - rect.top) / rect.height - 0.5;
    heroCard.style.transform = `rotateX(${relY * -6}deg) rotateY(${relX * 6}deg) translateY(0)`;
    heroCard.style.boxShadow = `${-relX * 25}px ${Math.abs(relY) * 20 + 20}px 60px rgba(12, 6, 32, 0.55)`;
  }

  function resetParallax() {
    if (!heroCard) return;
    heroCard.style.transform = "rotateX(0deg) rotateY(0deg) translateY(0)";
    heroCard.style.boxShadow = "0 30px 60px rgba(5, 3, 16, 0.55)";
  }

  function initParallax() {
    if (!heroSection || !heroCard) return;
    heroSection.addEventListener("pointermove", handleParallax);
    heroSection.addEventListener("pointerleave", resetParallax);
  }

  function initRevealAnimations() {
    const revealTargets = document.querySelectorAll(".feature-card, .download-card");
    if (!revealTargets.length) return;

    const observer = new IntersectionObserver(
      (entries) => {
        entries.forEach((entry) => {
          if (entry.isIntersecting) {
            entry.target.classList.add("is-visible");
            observer.unobserve(entry.target);
          }
        });
      },
      { threshold: 0.2, rootMargin: "0px 0px -10% 0px" }
    );

    revealTargets.forEach((el) => observer.observe(el));
  }

  window.addEventListener("resize", handleResize);
  document.addEventListener("DOMContentLoaded", () => {
    initIcons();
    initParallax();
    initRevealAnimations();
  });
})();
