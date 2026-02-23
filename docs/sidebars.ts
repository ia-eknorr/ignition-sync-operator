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
        "configuration/stoker-cr",
        "configuration/sync-profile",
        "configuration/helm-values",
      ],
    },
    "roadmap",
  ],
};

export default sidebars;
