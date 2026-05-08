// OpsCore v2.0 - Azure Infrastructure
// Bicep template for serverless Azure deployment
// Uses Azure free tier resources where possible

@description('Environment name (staging/production)')
@allowed(['staging', 'prod'])
param environment string = 'staging'

@description('Azure region for resources')
param location string = 'eastus'

@description('Base name for resources')
param baseName string = 'opscore'

// =============================================================================
// Variables
// =============================================================================
var resourcePrefix = '${baseName}-${environment}'
var storageAccountName = replace('${resourcePrefix}storage', '-', '')
var appInsightsName = '${resourcePrefix}-insights'
var cosmosAccountName = replace('${resourcePrefix}cosmos', '-', '')
var functionAppName = '${resourcePrefix}-func'
var planName = '${resourcePrefix}-asp'

// =============================================================================
// Resource Group
// =============================================================================
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' existing = {
  name: 'opscore-${environment}-rg'
}

// =============================================================================
// Storage Account - Blob + Queue services
// =============================================================================
resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: storageAccountName
  location: location
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'StorageV2'
  properties: {
    supportsHttpsTrafficOnly: true
    minimumTlsVersion: 'TLS1_2'
    allowBlobPublicAccess: false
    networkAcls: {
      defaultAction: 'Allow'
    }
  }
}

// Blob services
resource blobServices 'Microsoft.Storage/storageAccounts/blobServices@2023-01-01' = {
  parent: storageAccount
  name: 'default'
  properties: {
    cors: {
      corsRules: []
    }
    deleteRetentionPolicy: {
      enabled: true
      days: 7
    }
  }
}

// Queue services
resource queueServices 'Microsoft.Storage/storageAccounts/queueServices@2023-01-01' = {
  parent: storageAccount
  name: 'default'
  properties: {
    cors: {
      corsRules: []
    }
  }
}

// Storage Account Keys
output storageConnectionString string = listKeys(storageAccount.id, storageAccount.apiVersion).keys[0].value
output storageAccountKey string = listKeys(storageAccount.id, storageAccount.apiVersion).keys[0].value

// =============================================================================
// Storage Containers (Blob)
// =============================================================================
resource documentsContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = {
  parent: blobServices
  name: 'documents'
  properties: {
    containerName: 'documents'
    publicAccess: 'None'
    metadata: {
      environment: environment
    }
  }
}

resource extractsContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = {
  parent: blobServices
  name: 'extracts'
  properties: {
    containerName: 'extracts'
    publicAccess: 'None'
    metadata: {
      environment: environment
    }
  }
}

resource exportsContainer 'Microsoft.Storage/storageAccounts/blobServices/containers@2023-01-01' = {
  parent: blobServices
  name: 'exports'
  properties: {
    containerName: 'exports'
    publicAccess: 'None'
    metadata: {
      environment: environment
    }
  }
}

// =============================================================================
// Storage Queues
// =============================================================================
resource documentQueue 'Microsoft.Storage/storageAccounts/queueServices/queues@2023-01-01' = {
  parent: queueServices
  name: 'document-jobs'
  properties: {
    metadata: {
      environment: environment
    }
  }
}

resource vendorQueue 'Microsoft.Storage/storageAccounts/queueServices/queues@2023-01-01' = {
  parent: queueServices
  name: 'vendor-jobs'
  properties: {
    metadata: {
      environment: environment
    }
  }
}

resource complianceQueue 'Microsoft.Storage/storageAccounts/queueServices/queues@2023-01-01' = {
  parent: queueServices
  name: 'compliance-jobs'
  properties: {
    metadata: {
      environment: environment
    }
  }
}

resource deadLetterQueue 'Microsoft.Storage/storageAccounts/queueServices/queues@2023-01-01' = {
  parent: queueServices
  name: 'dead-letter'
  properties: {
    metadata: {
      environment: environment
    }
  }
}

// =============================================================================
// Application Insights
// =============================================================================
resource appInsights 'Microsoft.Insights/components@2020-02-02' = {
  name: appInsightsName
  location: location
  kind: 'web'
  properties: {
    Application_Type: 'web'
    Request_Source: 'rest'
    RetentionInDays: 30
    publicNetworkAccessForIngestion: 'Enabled'
    publicNetworkAccessForQuery: 'Enabled'
  }
}

output appInsightsInstrumentationKey string = appInsights.properties.InstrumentationKey
output appInsightsAppId string = appInsights.properties.AppId

// =============================================================================
// Azure Cosmos DB - NoSQL
// =============================================================================
resource cosmosDb 'Microsoft.DocumentDB/databaseAccounts@2024-08-01' = {
  name: cosmosAccountName
  location: location
  kind: 'GlobalDocumentDB'
  properties: {
    databaseAccountOfferType: 'Standard'
    enableAutomaticFailover: false
    enableFreeTier: true
    consistencyPolicy: {
      defaultConsistencyLevel: 'Session'
      maxStalenessPrefix: 100
      maxIntervalInMs: 300
    }
    locations: [
      {
        locationName: location
        failoverPriority: 0
        isZoneRedundant: false
      }
    ]
    capabilities: []
  }
}

resource sqlDatabase 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases@2024-08-01' = {
  parent: cosmosDb
  name: 'opscore'
  properties: {
    resource: {
      id: 'opscore'
    }
    options: {
      '--throughput': 1000
    }
  }
}

// Cosmos DB Collections (Jobs)
resource jobsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'jobs'
  properties: {
    resource: {
      id: 'jobs'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (Documents)
resource documentsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'documents'
  properties: {
    resource: {
      id: 'documents'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (Vendors)
resource vendorsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'vendors'
  properties: {
    resource: {
      id: 'vendors'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (Compliance Chunks)
resource complianceChunksCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'compliance_chunks'
  properties: {
    resource: {
      id: 'compliance_chunks'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (Audit Events)
resource auditEventsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'audit_events'
  properties: {
    resource: {
      id: 'audit_events'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (HITL Requests)
resource hitlRequestsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'hitl_requests'
  properties: {
    resource: {
      id: 'hitl_requests'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

// Cosmos DB Collections (Settings)
resource settingsCollection 'Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers@2024-08-01' = {
  parent: sqlDatabase
  name: 'settings'
  properties: {
    resource: {
      id: 'settings'
      partitionKey: {
        paths: ['/tenant_id']
        kind: 'Hash'
      }
    }
    options: {
      throughput: 1000
    }
  }
}

output cosmosDbEndpoint string = cosmosDb.properties.documentEndpoint
output cosmosDbPrimaryKey string = listKeys(cosmosDb.id, cosmosDb.apiVersion).primaryMasterKey

// =============================================================================
// App Service Plan - Consumption (Always Free Tier)
// =============================================================================
resource appServicePlan 'Microsoft.Web/serverfarms@2023-12-01' = {
  name: planName
  location: location
  sku: {
    name: 'Y1'
    tier: 'Free'
    size: 'Y1'
    family: 'Y'
    capacity: 0
  }
  properties: {
    computeMode: 'Consumption'
    reserved: false
  }
}

// =============================================================================
// Azure Functions - Consumption Plan
// =============================================================================
resource functionApp 'Microsoft.Web/sites@2023-12-01' = {
  name: functionAppName
  location: location
  kind: 'functionapp'
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    serverFarmId: appServicePlan.id
    siteConfig: {
      appSettings: [
        {
          name: 'AzureWebJobsStorage'
          value: '@Microsoft.KeyVault(secretUri=https://${resourcePrefix}-kv.vault.azure.net/secrets/storage-connection-string/)'
        }
        {
          name: 'WEBSITE_CONTENTSHARE'
          value: resourcePrefix
        }
        {
          name: 'APPINSIGHTS_INSTRUMENTATIONKEY'
          value: appInsights.properties.InstrumentationKey
        }
        // NOTE: For custom handlers, DO NOT set FUNCTIONS_WORKER_RUNTIME
        // Setting it causes the host to try to load a language worker instead of 
        // starting the custom handler executable
        {
          name: 'FUNCTIONS_EXTENSION_VERSION'
          value: '~4'
        }
        {
          name: 'COSMOS_DB_ENDPOINT'
          value: cosmosDb.properties.documentEndpoint
        }
        {
          name: 'COSMOS_DB_KEY'
          value: listKeys(cosmosDb.id, cosmosDb.apiVersion).primaryMasterKey
        }
        {
          name: 'COSMOS_DB_DATABASE'
          value: 'opscore'
        }
        {
          name: 'ENVIRONMENT'
          value: environment
        }
      ]
      ftpsState: 'Disabled'
      http20Enabled: true
      minTlsVersion: '1.2'
      appCommandLine: ''
    }
    httpsOnly: true
  }
}

output functionAppName string = functionApp.name
output functionAppDefaultHost string = functionApp.properties.defaultHostName

// =============================================================================
// Azure Static Web Apps (Placeholder)
// Note: Static Web Apps require manual creation or separate Bicep due to 
//       managed identity requirements. Use Azure Portal for initial setup.
// =============================================================================
// This is a placeholder resource - uncomment and configure after initial setup
/*
resource staticWebApp 'Microsoft.Web/staticSites@2023-01-01' = {
  name: '${resourcePrefix}-webapp'
  location: location
  properties: {
    sku: {
      name: 'Standard'
      tier: 'Standard'
    }
  }
}
*/

// =============================================================================
// Key Vault (for secrets management)
// =============================================================================
resource keyVault 'Microsoft.KeyVault/vaults@2023-02-01' = {
  name: '${resourcePrefix}-kv'
  location: location
  properties: {
    sku: {
      family: 'A'
      name: 'standard'
    }
    tenantId: subscription().tenantId
    enableRpfAuthorization: true
    enableSoftDelete: true
    softDeleteRetentionInDays: 90
    enablePurgeProtection: false
    networkAcls: {
      defaultAction: 'Allow'
    }
    secrets: [
      {
        name: 'storage-connection-string'
        properties: {
          attributes: {
            enabled: true
          }
        }
      }
    ]
  }
}

output keyVaultUri string = keyVault.properties.vaultUri

// =============================================================================
// Outputs Summary
// =============================================================================
output deploymentSummary object = {
  environment: environment
  location: location
  resources: {
    storageAccount: storageAccountName
    cosmosDb: cosmosAccountName
    functionApp: functionAppName
    appInsights: appInsightsName
    keyVault: keyVault.name
  }
  queues: [
    'document-jobs'
    'vendor-jobs'
    'compliance-jobs'
    'dead-letter'
  ]
  containers: [
    'documents'
    'extracts'
    'exports'
  ]
  cosmosCollections: [
    'jobs'
    'documents'
    'vendors'
    'compliance_chunks'
    'audit_events'
    'hitl_requests'
    'settings'
  ]
}