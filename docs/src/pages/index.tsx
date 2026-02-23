import clsx from "clsx";
import Link from "@docusaurus/Link";
import useDocusaurusContext from "@docusaurus/useDocusaurusContext";
import Layout from "@theme/Layout";

const features = [
  {
    title: "Git-Driven Sync",
    description:
      "Manage Ignition gateway projects, tags, and resources in Git. Stoker continuously syncs configuration to your gateways.",
  },
  {
    title: "No Shared Storage",
    description:
      "The controller resolves refs via ls-remote. The agent sidecar clones independently to a local emptyDir — no PVCs required.",
  },
  {
    title: "Automatic Sidecar Injection",
    description:
      "A MutatingWebhook injects the sync agent into annotated pods. Just add an annotation and Stoker handles the rest.",
  },
];

function Hero() {
  const { siteConfig } = useDocusaurusContext();
  return (
    <header className="hero hero--primary">
      <div className="container">
        <img
          src="img/logo.png"
          alt="Stoker logo"
          width="140"
          style={{ marginBottom: "1rem" }}
        />
        <h1 className="hero__title">{siteConfig.title}</h1>
        <p className="hero__subtitle">{siteConfig.tagline}</p>
        <div style={{ marginTop: "1.5rem" }}>
          <Link
            className="button button--secondary button--lg"
            to="/quickstart"
          >
            Get Started
          </Link>
        </div>
      </div>
    </header>
  );
}

function Features() {
  return (
    <section className="features">
      <div className="container">
        <div className="row">
          {features.map((f, idx) => (
            <div key={idx} className={clsx("col col--4")}>
              <div className="feature">
                <h3>{f.title}</h3>
                <p>{f.description}</p>
              </div>
            </div>
          ))}
        </div>
      </div>
    </section>
  );
}

export default function Home(): JSX.Element {
  const { siteConfig } = useDocusaurusContext();
  return (
    <Layout title={siteConfig.title} description={siteConfig.tagline}>
      <Hero />
      <main>
        <Features />
      </main>
    </Layout>
  );
}
