if (typeof window !== "undefined") {
  document.addEventListener("keydown", (e) => {
    const isCmdK = (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k";
    const isSlash = e.key === "/" && !e.metaKey && !e.ctrlKey && !e.altKey;

    if (!isCmdK && !isSlash) return;

    const tag = document.activeElement?.tagName;
    if (
      tag === "INPUT" ||
      tag === "TEXTAREA" ||
      document.activeElement?.isContentEditable
    ) {
      return;
    }

    e.preventDefault();

    const input = document.querySelector(".dsla-search-wrapper .aa-Input");
    if (input) {
      input.focus();
    }
  });
}
