import {
  lazy,
  Suspense,
  type ComponentType,
  type ReactNode,
} from "react";

type LoaderResult<P> =
  | ComponentType<P>
  | { default: ComponentType<P> };

type DynamicOptions = {
  ssr?: boolean;
  loading?: ComponentType;
};

export default function dynamic<P extends object = Record<string, never>>(
  loader: () => Promise<LoaderResult<P>>,
  options: DynamicOptions = {},
): ComponentType<P> {
  const LazyComponent = lazy(async () => {
    const loaded = await loader();
    if (typeof loaded === "function") {
      return { default: loaded };
    }
    return loaded;
  });

  const Loading = options.loading;

  return function DynamicComponent(props: P): ReactNode {
    return (
      <Suspense fallback={Loading ? <Loading /> : null}>
        <LazyComponent {...props} />
      </Suspense>
    );
  };
}
