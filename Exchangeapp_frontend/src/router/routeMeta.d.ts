import 'vue-router';

declare module 'vue-router' {
  interface RouteMeta {
    layout?: 'app' | 'auth';
    title?: string;
    guestOnly?: boolean;
  }
}

export {};
