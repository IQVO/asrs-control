import type { SidebarsConfig } from "@docusaurus/plugin-content-docs";

const sidebar: SidebarsConfig = {
  apisidebar: [
    {
      type: "doc",
      id: "api-reference/rest/asrs-control-api",
    },
    {
      type: "category",
      label: "storage-jobs",
      link: {
        type: "doc",
        id: "api-reference/rest/storage-jobs",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/create-storage-job",
          label: "Request a store or a retrieve",
          className: "api-method post",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-storage-job",
          label: "Read a storage job",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/cancel-storage-job",
          label: "Cancel a storage job",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "asrs",
      link: {
        type: "doc",
        id: "api-reference/rest/asrs",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-asrs",
          label: "Read an AS/RS",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/confirm-port-pickup",
          label: "Confirm a container was taken from an output port",
          className: "api-method post",
        },
      ],
    },
    {
      type: "category",
      label: "health",
      link: {
        type: "doc",
        id: "api-reference/rest/health",
      },
      items: [
        {
          type: "doc",
          id: "api-reference/rest/get-liveness",
          label: "Liveness",
          className: "api-method get",
        },
        {
          type: "doc",
          id: "api-reference/rest/get-readiness",
          label: "Readiness",
          className: "api-method get",
        },
      ],
    },
  ],
};

export default sidebar.apisidebar;
