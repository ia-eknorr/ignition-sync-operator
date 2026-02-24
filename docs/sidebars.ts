import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebars: SidebarsConfig = {
  docs: [
    "quickstart",
    "installation",
    {
      type: "category",
      label: "Configuration",
      collapsed: false,
      items: [
        "configuration/gatewaysync-cr",
        "configuration/helm-values",
      ],
    },
    "roadmap",
  ],
};

export default sidebars;
