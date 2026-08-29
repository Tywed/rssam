package smotrim

const defaultGraphQLURL = "https://apis.smotrim.ru/graphql"

const brandMorePlotsQuery = `query BrandMorePlots($id: Int!, $limit: Int!, $page: Int!) {
  brand(id: $id) {
    title
    plotsPaginate(first: $limit, page: $page) {
      data {
        id
        title
        publishedAt
        episode {
          id
          title
          publicationDate
          airDate
        }
        images(linkTypes: [SplashScreen], presets: [Large]) {
          ... on Image {
            presets { name link }
          }
        }
      }
    }
  }
}`

const episodesQuery = `query EpisodesQuery($brandId: Int, $order: SortOrder = DESC, $first: Int = 50, $page: Int = 1) {
  episodesFilter(
    brand_id: $brandId
    orderBy: { column: EPISODES_AIR_DATE, order: $order }
    first: $first
    page: $page
  ) {
    data {
      id
      title
      description
      airDate
      publicationDate
      brand { id title }
      fullVideo {
        ... on Video {
          id
          publicId
          duration
          videoType
          images(linkTypes: [Main, SplashScreen], presets: [Large]) {
            ... on Image {
              presets { name link }
            }
          }
        }
      }
      images(linkTypes: [Main, SplashScreen], presets: [Large]) {
        ... on Image {
          presets { name link }
        }
      }
    }
  }
}`
