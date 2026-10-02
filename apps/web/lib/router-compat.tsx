import {
  Link as TanStackLink,
  useNavigate as useTanStackNavigate,
  useParams as useTanStackParams,
  useRouterState,
} from "@tanstack/react-router";
import type { AnchorHTMLAttributes, ReactNode } from "react";

export type CompatHref =
  | string
  | {
      pathname?: string;
      query?: Record<string, string | number | boolean | null | undefined>;
      hash?: string;
    };

type LinkProps = Omit<AnchorHTMLAttributes<HTMLAnchorElement>, "href"> & {
  href: CompatHref;
  children?: ReactNode;
  replace?: boolean;
  scroll?: boolean;
  prefetch?: boolean | "auto" | null;
};

function hrefToString(href: CompatHref): string {
  if (typeof href === "string") return href;

  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(href.query ?? {})) {
    if (value !== null && value !== undefined) params.set(key, String(value));
  }
  const query = params.size > 0 ? `?${params.toString()}` : "";
  const hash = href.hash ? `#${href.hash.replace(/^#/, "")}` : "";
  return `${href.pathname ?? "/"}${query}${hash}`;
}

export default function Link({
  href,
  replace,
  scroll: _scroll,
  prefetch: _prefetch,
  ...props
}: LinkProps) {
  return (
    <TanStackLink
      {...(props as Record<string, unknown>)}
      to={hrefToString(href) as never}
      replace={replace}
    />
  );
}

export function usePathname(): string {
  return useRouterState({ select: (state) => state.location.pathname });
}

export function useParams<T extends Record<string, string | string[]>>() {
  return useTanStackParams({ strict: false }) as unknown as T;
}

export function useRouter() {
  const navigate = useTanStackNavigate();

  return {
    push: (href: string) => navigate({ to: href as never }),
    replace: (href: string) => navigate({ to: href as never, replace: true }),
    back: () => window.history.back(),
    forward: () => window.history.forward(),
    refresh: () => window.location.reload(),
    prefetch: async (_href: string) => undefined,
  };
}
