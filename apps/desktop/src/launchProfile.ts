// Public environment labels only. Authorization and networking belong to Go.
const profiles = {
  production: {
    label: '生产环境',
    control: 'https://lightflow.aibusinesses.cc',
    gateway: 'lightflow-gw.aibusinesses.cc（端口由 API 提供）',
  },
  test: {
    label: '测试环境',
    control: 'https://192.168.194.128:8443',
    gateway: '192.168.194.128:4433/udp',
  },
};

const selected: unknown = import.meta.env.VITE_LIGHTFLOW_PROFILE;
export const launchProfile = selected === 'production' || selected === 'test' ? profiles[selected] : undefined;
