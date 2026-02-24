import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docs: [
    {
      type: "category",
      label: "Overview",
      collapsed: false,
      items: ["overview/introduction", "overview/architecture"],
    },
    {
      type: "category",
      label: "Getting Started",
      collapsed: false,
      items: ["quickstart", "installation"],
    },
    {
      type: "category",
      label: "Guides",
      collapsed: false,
      items: [
        "guides/git-authentication",
        "guides/multi-gateway",
        "guides/webhook-sync",
      ],
    },
    {
      type: "category",
      label: "Reference",
      collapsed: false,
      items: [
        "reference/gatewaysync-cr",
        "reference/helm-values",
        "reference/annotations",
        "reference/troubleshooting",
      ],
    },
    "roadmap",
  ],
};

export default sidebars;
